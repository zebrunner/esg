package main

import (
	"flag"
	"net/http"
	"net/http/httputil"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
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

func CreateRouter() *gin.Engine {
	r := gin.New()
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

	hub := r.Group("/")
	hub.Any("/wd/hub/*action", ReverseProxy())
	{
		hub.GET("/", handlers.Welcome)
		hub.GET("/ping", handlers.Ping)

		// sessionId passed for linux browsers and redroid session. taskId passed for cypress
		hub.GET("/ws/vnc/:id", func(c *gin.Context) {
			handler := websocket.Handler(handlers.Vnc)
			c.Request.Header.Add("Access-Control-Allow-Origin", "*")
			c.Request.Header.Add("X-Real-IP", c.Request.RemoteAddr)

			log.WithField("request", c.Request).Debug("Vnc request")
			handler.ServeHTTP(c.Writer, c.Request)
		})
	}

	seleniumHub := hub.Group("/", handlers.SeleniumError)
	{
		seleniumHub.POST("/session", handlers.Create) // Auth logic moved to handler
		seleniumHub.DELETE("/session/:session", handlers.CloseSession)
		seleniumHub.Any("/session/:session/*action", handlers.Proxy)

		seleniumHub.GET("/download/:session/:file", handlers.Downloads)
		seleniumHub.GET("/download/:session", handlers.Downloads)
		seleniumHub.DELETE("/download/:session/:file", handlers.Downloads)
		seleniumHub.HEAD("/download/:session/:file", handlers.Downloads)

		seleniumHub.GET("/clipboard/:session", handlers.Clipboard)
		seleniumHub.POST("/clipboard/:session", handlers.Clipboard)

		seleniumHub.GET("/devtools/:session", handlers.Devtools)

		seleniumHub.DELETE("/tasks/:task", handlers.AbortTask) // to be able to abort generic tasks by taskId
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

func refreshIMDSV2Token() {
	for {
		err := utils.RefreshIMDSV2Token()
		if err != nil {
			log.WithError(err).Error("Failed to generate IMDSV2 token")
		} else {
			log.Debug("Successfully generated IMDSV2 token")
		}
		time.Sleep(2*time.Hour + 30*time.Minute)
	}
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
		log.WithError(err).Fatal("Failed to init DB client! Stopping router...")
		os.Exit(1)
	}

	defer config.DbConnection.Close()

	err = config.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init Redis client! Stopping router...")
		os.Exit(1)
	}

	defer config.RedisSessionsConnection.Close()
	defer config.RedisTasksConnection.Close()
	defer config.RedisDefinitionConnection.Close()

	aws, err := service.InitAws()
	if err != nil {
		log.WithError(err).Fatal("Failed to start aws session! Stopping router...")
		os.Exit(1)
	}
	service.AwsSess = aws

	if config.Conf.Imdsv2Enabled {
		go refreshIMDSV2Token()
	}

	router := CreateRouter()

	for {
		if definitionmap.IsRefreshDone() {
			break
		}
		time.Sleep(5 * time.Second)
	}
	
	log.Infof("Listening on %s", listen)
	err = router.Run(listen)
	if err != nil {
		log.WithError(err).Fatal("Failed to start server")
	}
}
