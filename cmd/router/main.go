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
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
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

func getRouterBasis() *gin.Engine {
	r := gin.New()
	r.ForwardedByClientIP = true

	r.Use(gin.LoggerWithFormatter(utils.TraceLogFromating), gin.Recovery())

	lowLvlApi := r.Group("/api", handlers.APIError, handlers.LowLvlAuthentication)
	{
		lowLvlApi.POST("/users", handlers.CreateUser)
		lowLvlApi.DELETE("/users/:username", handlers.DeleteUser)
		lowLvlApi.PUT("/users/:username/refresh-token", handlers.RefreshToken)
		lowLvlApi.PUT("/users/:username/activation", handlers.UserActivation)
		lowLvlApi.GET("/logs/:session", handlers.Logs)
		lowLvlApi.GET("/video/:session", handlers.Video)
		lowLvlApi.GET("/tasks/:task/log", handlers.TaskLog)
		lowLvlApi.GET("/tasks/:task/status", handlers.TaskDescribe)
	}

	return r
}

func CreateMockRouter() *gin.Engine {
	r := getRouterBasis()

	hub := r.Group("/")
	hub.Any("/wd/hub/*action", ReverseProxy())
	{
		hub.GET("/", handlers.WelcomeWithInstallationRef)
		hub.GET("/ping", handlers.Ping)
	}

	return r
}

func CreateRouter() *gin.Engine {
	r := getRouterBasis()

	hub := r.Group("/")
	hub.Any("/wd/hub/*action", ReverseProxy())
	{
		hub.GET("/", handlers.Welcome)
		hub.GET("/ping", handlers.Ping)
	}

	selenium := hub.Group("/", handlers.SeleniumError)
	{
		selenium.POST("/session", handlers.Create) // Auth logic moved to handler
		selenium.GET("/ws/vnc/:uuid", handlers.ValidateMapperPresence, handlers.Vnc)

		genericHub := selenium.Group("/", handlers.ValidateGenericMapperPresence, handlers.LockGenericTaskCache)
		{
			genericHub.POST("/tasks/:uuid", handlers.MarkAsFinished)
			genericHub.DELETE("/tasks/:uuid", handlers.AbortTask) // to be able to abort generic tasks by uuid
		}

		cachedSeleniumSession := selenium.Group("/", handlers.ValidateMapperPresence, handlers.ValidateSessionStatus, handlers.UpdateLastAccessTime)
		{
			cachedSeleniumSession.DELETE("/session/:uuid", handlers.CloseSession)
			cachedSeleniumSession.Any("/session/:uuid/*action", handlers.Proxy)

			cachedSeleniumSession.Any("/download/:uuid/*action", handlers.Downloads)

			cachedSeleniumSession.GET("/clipboard/:uuid", handlers.Clipboard)
			cachedSeleniumSession.POST("/clipboard/:uuid", handlers.Clipboard)

			proxyHandlerHub := cachedSeleniumSession.Group("/proxy/:uuid", handlers.ProxyMitm)
			{
				proxyHandlerHub.GET("/download/har/:flow")
				proxyHandlerHub.GET("/download/dump/:flow")
				proxyHandlerHub.POST("/mitm-restart")
				proxyHandlerHub.DELETE("/clear-flows")
			}

			devtoolsHub := cachedSeleniumSession.Group("/devtools/:uuid", handlers.Devtools)
			{
				devtoolsHub.GET("/")
				devtoolsHub.GET("/browser")
				devtoolsHub.GET("/page")
				devtoolsHub.GET("/page/:target-id")
			}
		}
	}

	httpHub := hub.Group("/", handlers.APIError)
	{
		httpHub.GET("/status", handlers.APIAuthentication, handlers.ClusterStatus)
		httpHub.GET("/browsers", handlers.ListDrivers)
		httpHub.GET("/logs/:session", handlers.Logs)
		httpHub.GET("/video/:session", handlers.Video)
		httpHub.GET("/tasks/:task/log", handlers.TaskLog)
		httpHub.GET("/tasks/:task/status", handlers.LowLvlAuthentication, handlers.TaskDescribe)
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

	scalersMap, err := service.InitScalingData()
	if err != nil {
		log.WithError(err).Error("Failed to init scaling data")
		return nil, err
	}

	for capacityProvider, scaler := range scalersMap {
		environment.CapacityProvdirResourcesLimit[capacityProvider] = environment.Resources{Cpu: scaler.InstanceResources.CPU, Memory: scaler.InstanceResources.Memory}
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

	if config.Conf.ProductionEnv {
		gin.SetMode(gin.ReleaseMode)
	}

	err := config.InitDBConnection(config.Conf.DbConnectionString)
	if err != nil {
		utils.ExitWithError(err, "Failed to init DB client", log.NewEntry(log.StandardLogger()))
	}
	defer config.DbConnection.Close()

	err = config.InitCache()
	if err != nil {
		utils.ExitWithError(err, "Failed to init Redis client", log.NewEntry(log.StandardLogger()))
	}

	defer config.RedisMapperClient.Close()
	defer config.RedisDefinitionClient.Close()
	defer config.RedisResourcesClient.Close()
	defer config.RedisUtilityClient.Close()
	mapper.InitMapperWorkers()
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
		if utilsmap.IsTaskDefenitionRefreshDone() {
			definitionmap.SaveAndUpdateDefinitions()
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
