package main

import (
	"flag"
	"fmt"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/handlers"
	"github.com/zebrunner/esg/service"
)

var (
	listen             string
	gracefulPeriod     time.Duration
	retryCount         int
	dbConnectionString string
)

func init() {
	flag.BoolVar(&handlers.EnableFileUpload, "enable-file-upload", false, "File upload support")
	flag.StringVar(&listen, "listen", ":4444", "Network address to accept connections")
	flag.IntVar(&retryCount, "retry-count", 1, "New session attempts retry count")
	flag.DurationVar(&handlers.Timeout, "timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	flag.DurationVar(&handlers.MaxTimeout, "max-timeout", 1*time.Hour, "Maximum valid session idle timeout in time.Duration format")
	flag.DurationVar(&handlers.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.DurationVar(&handlers.ServiceStartupTimeout, "service-startup-timeout", 30*time.Second, "Service startup timeout in time.Duration format")
	flag.StringVar(&handlers.VideoRecorderImage, "video-recorder-image", "selenoid/video-recorder:latest-release", "Image to use as video recorder")
	flag.DurationVar(&gracefulPeriod, "graceful-period", 300*time.Second, "graceful shutdown period in time.Duration format, e.g. 300s or 500ms")
	// AWS Related args
	flag.StringVar(&service.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&service.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&service.AwsCluster, "aws-cluster", "esg", "AWS cluster name")
	flag.StringVar(&service.AwsElasticCache, "aws-elastic-cache", "localhost:6379", "AWS elastic cache connection URL")
	flag.StringVar(&service.AwsAutoScalingGroup, "aws-auto-scaling-group", "esg-asg", "AWS auto scaling group name")
	flag.IntVar(&service.MinMemory, "min-memory", 768, "AWS minimum memory limitation for session")
	flag.IntVar(&service.MinMemoryReservation, "min-memory-reservation", 768, "AWS minimum memory reservation limitation for session")
	flag.IntVar(&service.MaxMemory, "max-memory", 8192, "AWS maximum memory limitation for session")
	flag.IntVar(&service.MaxMemoryReservation, "max-memory-reservation", 8192, "AWS maximum memory reservation limitation for session")
	flag.IntVar(&service.MinCpu, "min-cpu", 512, "AWS minimum CPU limitation for session")
	flag.IntVar(&service.MaxCpu, "max-cpu", 4096, "AWS maximum CPU limitation for session")
	flag.StringVar(&service.S3Bucket, "s3-bucket", "", "S3 Bucket name for pushing artifacts")
	flag.StringVar(&service.Tenant, "tenant", "", "Zebrunner tenant name")
	flag.StringVar(&service.AwsAccessKeyID, "aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&service.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for S3 bucket")
	flag.StringVar(&dbConnectionString, "db-connection", "", "Connection string for database")

	flag.Parse()

	handlers.InitManager()
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
	r.Use(gin.LoggerWithFormatter(TraceLogFromating), gin.Recovery())

	api := r.Group("/api")
	api.Use(handlers.APIError)
	{
		api.POST("/users", handlers.CreateUser)
		api.DELETE("/users/:username", handlers.DeleteUser)
		api.PUT("/users/:username/refresh-token", handlers.RefreshToken)
		api.PUT("/users/:username/activation", handlers.UserActivation)
	}

	hub := r.Group("/")
	hub.Use(handlers.SeleniumError)
	{
		hub.GET("/", handlers.Welcome)
		hub.GET("/status", handlers.Authentication, handlers.ClusterStatus)
		hub.GET("/ping", handlers.Ping)

		hub.Any("/wd/hub/*action", ReverseProxy())
		hub.POST("/session", handlers.Authentication, handlers.Create)
		hub.Any("/session/*action", handlers.Proxy)
		hub.GET("/logs/:session", handlers.Logs)
		hub.GET("/video/:session", handlers.Video)

		hub.GET("/vnc/:session", func(c *gin.Context) {
			handler := websocket.Handler(handlers.Vnc)
			fmt.Printf("[VNC REQUEST] %+v", c.Request)
			handler.ServeHTTP(c.Writer, c.Request)
		})
		hub.GET("/ws/vnc/:session", func(c *gin.Context) {
			handler := websocket.Handler(handlers.Vnc)
			c.Request.Header.Add("Access-Control-Allow-Origin", "*")
			c.Request.Header.Add("X-Real-IP", c.Request.RemoteAddr)

			fmt.Printf("[VNC REQUEST] %+v", c.Request)
			handler.ServeHTTP(c.Writer, c.Request)
		})

		hub.GET("/download/:session/:file", handlers.Downloads)
		hub.DELETE("/download/:session/:file", handlers.Downloads)

		hub.GET("/clipboard/:session", handlers.Clipboard)
		hub.POST("/clipboard/:session", handlers.Clipboard)

		hub.GET("/devtools/:session", handlers.Devtools)
	}

	return r
}

func main() {
	db, err := service.InitDBConnection(dbConnectionString)
	if err != nil {
		log.WithError(err).Fatal("Failed to init DB client.")
	}
	service.DB = db
	defer db.Close()

	rdb, err := service.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init Redis client")
	}
	handlers.RDB = rdb
	defer rdb.Close()

	go handlers.ClearSessions()

	router := CreateRouter()
	err = router.Run(listen)
	if err != nil {
		log.WithError(err).Fatal("Failed to start server")
	}

	log.Infof("Listening on %s", listen)
}
