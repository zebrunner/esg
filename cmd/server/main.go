package main

import (
	"flag"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"

	log "github.com/sirupsen/logrus"
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

	api := r.Group("/api")
	api.Use(handlers.APIError)
	api.Use(handlers.APIAuthentication)
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
	hub.Use(handlers.SeleniumError)
	{
		hub.GET("/", handlers.Welcome)
		hub.GET("/status", handlers.Authentication, handlers.ClusterStatus)
		hub.GET("/ping", handlers.Ping)
		hub.GET("/browsers", handlers.ListDrivers)

		hub.Any("/wd/hub/*action", ReverseProxy())
		hub.POST("/session", handlers.Create) // Auth logic moved to handler
		hub.DELETE("/session/:session", handlers.CloseSession)
		hub.Any("/session/:session/*action", handlers.Proxy)

		hub.GET("/vnc/:session", func(c *gin.Context) {
			handler := websocket.Handler(handlers.Vnc)
			log.WithField("request", c.Request).Debug("Vnc request")
			handler.ServeHTTP(c.Writer, c.Request)
		})
		hub.GET("/ws/vnc/:session", func(c *gin.Context) {
			handler := websocket.Handler(handlers.Vnc)
			c.Request.Header.Add("Access-Control-Allow-Origin", "*")
			c.Request.Header.Add("X-Real-IP", c.Request.RemoteAddr)

			log.WithField("request", c.Request).Debug("Vnc request")
			handler.ServeHTTP(c.Writer, c.Request)
		})

		hub.GET("/download/:session/:file", handlers.Downloads)
		hub.GET("/download/:session", handlers.Downloads)
		hub.DELETE("/download/:session/:file", handlers.Downloads)
		hub.HEAD("/download/:session/:file", handlers.Downloads)

		hub.GET("/clipboard/:session", handlers.Clipboard)
		hub.POST("/clipboard/:session", handlers.Clipboard)

		hub.GET("/devtools/:session", handlers.Devtools)

                hub.DELETE("/tasks/:task", handlers.AbortTask) // to be able to abort generic executor task by taskId
	}

	hub.Use(handlers.APIError)
	{
		hub.GET("/logs/:session", handlers.Logs)
		hub.GET("/video/:session", handlers.Video)
                hub.GET("/tasks/:task/log", handlers.TaskLog)
                hub.GET("/tasks/:task/status", handlers.TaskDescribe)
	}

	return r
}

func main() {
	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339Nano,
		DisableColors:     true,
	})

	db, err := config.InitDBConnection(config.Conf.DbConnectionString)
	if err != nil {
		log.WithError(err).Fatal("Failed to init DB client.")
	}
	config.DbConnection = db
	defer db.Close()

	rdb, err := config.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init Redis client")
	}
	config.RedisConnection = rdb
	defer rdb.Close()

	aws, err := service.InitAws()
	if err != nil {
		log.WithError(err).Fatal("Failed to start aws session")
	}
	service.AwsSess = aws

	router := CreateRouter()
	log.Infof("Listening on %s", listen)
	err = router.Run(listen)
	if err != nil {
		log.WithError(err).Fatal("Failed to start server")
	}
}
