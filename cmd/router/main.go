package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/service/elbv2"
	"github.com/gin-gonic/gin"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/resourcesToAllocate"
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/handlers"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
)

var (
	listen string
)

func init() {
	flag.StringVar(&listen, "listen", ":4444", "Network address to accept connections")
}

func ReverseProxy() gin.HandlerFunc {
	// TODO: Replace hardcoded target
	target := "localhost" + listen
	return func(c *gin.Context) {
		director := func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = target
			req.URL.Path = strings.TrimPrefix(req.URL.Path, "/wd/hub")
			req.Host = target
		}
		proxy := &httputil.ReverseProxy{Director: director}
		proxy.ServeHTTP(c.Writer, c.Request)
	}
}

func getRoutetBasis() *gin.Engine {
	r := gin.New()
	r.ForwardedByClientIP = true

	r.Use(gin.LoggerWithFormatter(utils.TraceLogFromating), gin.Recovery())

	api := r.Group("/api", handlers.APIError, handlers.LowLvlAuthentication)
	{
		api.POST("/users", handlers.CreateUser)
		api.DELETE("/users/:username", handlers.DeleteUser)
		api.PUT("/users/:username/refresh-token", handlers.RefreshToken)
		api.PUT("/users/:username/activation", handlers.UserActivation)
		api.GET("/logs/:session", handlers.Logs)
		api.GET("/video/:session", handlers.Video)
		api.GET("/tasks/:task/log", handlers.TaskLog)
		api.GET("/tasks/:task/status", handlers.TaskDescribe)
	}

	return r
}

func CreateMockRouter() *gin.Engine {
	r := getRoutetBasis()

	hub := r.Group("/")
	hub.Any("/wd/hub/*action", ReverseProxy())
	{
		hub.GET("/", handlers.WelcomeWithInstallationRef)
		hub.GET("/ping", handlers.Ping)
	}

	return r
}

func CreateRouter() *gin.Engine {
	r := getRoutetBasis()

	hub := r.Group("/")
	hub.Any("/wd/hub/*action", ReverseProxy())
	{
		hub.GET("/", handlers.Welcome)
		hub.GET("/ping", handlers.Ping)
		// sessionId passed for linux browsers and redroid session. taskId passed for cypress
		hub.GET("/ws/vnc/:uuid", handlers.Vnc)
	}

	seleniumHub := hub.Group("/", handlers.SeleniumError)
	{
		seleniumHub.POST("/session", handlers.Create) // Auth logic moved to handler
		seleniumHub.DELETE("/session/:session", handlers.CloseSession)
		seleniumHub.Any("/session/:session/*action", handlers.Proxy)

		seleniumHub.Any("/download/:session/*action", handlers.Downloads)

		seleniumHub.GET("/clipboard/:session", handlers.Clipboard)
		seleniumHub.POST("/clipboard/:session", handlers.Clipboard)

		proxyHandlerHub := seleniumHub.Group("/proxy/:session", handlers.ProxyMitm)
		{
			proxyHandlerHub.GET("/download/har/:flow")
			proxyHandlerHub.GET("/download/dump/:flow")
			proxyHandlerHub.POST("/mitm-restart")
			proxyHandlerHub.DELETE("/clear-flows")
		}

		devtoolsHub := seleniumHub.Group("/devtools/:session", handlers.Devtools)
		{
			devtoolsHub.GET("/")
			devtoolsHub.GET("/browser")
			devtoolsHub.GET("/page")
			devtoolsHub.GET("/page/:target-id")
		}

		genericHub := seleniumHub.Group("/", handlers.LockGenericTaskCache)
		{
			genericHub.POST("/tasks/:task", handlers.MarkAsFinished)
			genericHub.DELETE("/tasks/:task", handlers.AbortTask) // to be able to abort generic tasks by taskId
		}
	}

	httpHub := hub.Group("/", handlers.APIError)
	{
		httpHub.GET("/status", handlers.APIAuthentication, handlers.ClusterStatus)
		httpHub.GET("/browsers", handlers.ListDrivers)
		httpHub.GET("/logs/:session", handlers.Logs)
		httpHub.GET("/video/:session", handlers.Video)
		httpHub.GET("/tasks/:task/log", handlers.TaskLog)
		httpHub.GET("/tasks/:task/status", handlers.TaskDescribe)
	}

	return r
}

func InitClusterInfo() (*elbv2.TargetGroup, error) {
	aws, err := service.InitAws()
	if err != nil {
		log.WithError(err).Error("Failed to start aws session")
		return nil, err
	}
	service.AwsSess = aws

	err = utils.RefreshIMDSV2Token()
	if err != nil {
		log.WithError(err).Error("Failed to generate IMDSV2 token")
		return nil, err
	} else {
		go func() {
			for {
				time.Sleep(2*time.Hour + 30*time.Minute)
				err := utils.RefreshIMDSV2Token()
				if err != nil {
					log.WithError(err).Error("Failed to generate IMDSV2 token")
				} else {
					log.Debug("Successfully generated IMDSV2 token")
				}
			}
		}()
	}

	l := log.WithField("targetGroup", config.Conf.AwsTargetGroup)
	targetGroup, err := service.DescribeTargetGroup(config.Conf.AwsTargetGroup)
	if err != nil {
		l.WithError(err).Error("Failed to describe target group")
		return nil, err
	} else if len(targetGroup.LoadBalancerArns) < 1 || targetGroup.LoadBalancerArns[0] == nil {
		err = fmt.Errorf("target group is not attached to load balancer")
		l.WithError(err).Error("Failed to describe target group")
		return nil, err
	}

	loadBalancer, err := service.DescribeLoadBalancer(*targetGroup.LoadBalancerArns[0])
	if err != nil {
		l.WithError(err).Error("Failed to describe load balancer")
		return nil, err
	} else if loadBalancer.DNSName == nil || *loadBalancer.DNSName == "" {
		err = fmt.Errorf("load balancer without public dns")
		l.WithError(err).Error("Failed to describe load balancer")
		return nil, err
	}
	config.Conf.AwsEsgDns = *loadBalancer.DNSName

	return targetGroup, nil
}

func main() {
	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp: true,
		DisableColors: true,
	})

	err := config.InitDBConnection(config.Conf.DbConnectionString)
	if err != nil {
		utils.ExitWithError(err, "Failed to init DB client", log.NewEntry(log.StandardLogger()))
	}
	defer config.DbConnection.Close()

	err = config.InitCache()
	if err != nil {
		utils.ExitWithError(err, "Failed to init Redis client", log.NewEntry(log.StandardLogger()))
	}
	defer config.RedisSessionsClient.Close()
	defer config.RedisTasksClient.Close()
	defer config.RedisDefinitionClient.Close()
	defer config.RedisCypressSetClient.Close()
	defer config.RedisIdMapperClient.Close()
	defer config.RedisResourcesClient.Close()
	mapper.InitUUIDMapWorkers()
	taskmap.InitTaskmapWorkers()
	sessionmap.InitSessionmapWorker()
	resourcesToAllocate.InitResourceWorker()

	targetGroup, err := InitClusterInfo()
	if err != nil {
		log.WithError(err).Error("Failed to init cluster info")
		startMockRouter()
	} else {
		startRouter(targetGroup)
	}

	log.Info("Router exited")
}

func startRouter(targetGroup *elbv2.TargetGroup) {
	// wait until task definitions are updated
	for {
		if definitionmap.IsRefreshDone() {
			break
		}
		time.Sleep(5 * time.Second)
	}

	// init all service workers
	service.InitInstanceWorker()
	service.InitWaitWorker()

	// create sigterm listener chan
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	// wrapping router by http.Server object and starting it in new thread to wait for quit chan signal
	srv := &http.Server{
		Addr:    listen,
		Handler: CreateRouter(),
	}

	go func() {
		log.Infof("Listening on %s", listen)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.WithError(err).Fatal("Failed to start router")
		}
	}()

	l := log.WithField("targetGroup", config.Conf.AwsTargetGroup)
	err := service.RegisterTarget(targetGroup, config.Conf.ExternalPort)
	if err != nil {
		utils.ExitWithError(err, "Failed to append target to the elb target group", l)
	} else {
		// wait until alb actually starts distributing requests to that specific target
		// average time is between 5 to 15 seconds
		time.Sleep(25 * time.Second)
		l.Info("Registered target in target group")
	}
	log.Info("Service started")

	<-quit

	log.Info("Shutdown router ...")
	ctx, cancel := context.WithTimeout(context.Background(), config.Conf.ServiceStartupTimeout+5*time.Second)
	defer cancel()

	err = service.DeregisterTarget(targetGroup, config.Conf.ExternalPort)
	if err != nil {
		l.WithError(err).Fatal("Failed to detach target from the elb target group")
	} else {
		// wait until alb actually stops distributing requests to that specific target
		// average time is between 5 to 15 seconds
		time.Sleep(25 * time.Second)
		l.Info("Deregistered target from target group")
	}

	log.Info("finalizing connections...")
	if err := srv.Shutdown(ctx); err != nil {
		log.WithError(err).Error("Failed to shutdown correctly")
	}

	var wg sync.WaitGroup
	for routerUUID, ctx := range service.GenericCtxWorker.CtxMap {
		wg.Add(1)
		go func(routerUUID string, ctx context.Context) {
			log.WithField(config.RouterUUID, routerUUID).Info("Waiting for task to start")
			<-ctx.Done()
			log.WithField(config.RouterUUID, routerUUID).Info("Task started")
			wg.Done()
		}(routerUUID, ctx)
	}

	wg.Wait()
}

func startMockRouter() {
	// create sigterm listener chan
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	log.Infof("Starting mock listener on %s", listen)
	go CreateMockRouter().Run(listen)
	
	<-quit

	log.Info("Shutdown router ...")
}
