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
	listen         string
	gracefulPeriod time.Duration
)

func init() {
	flag.BoolVar(&config.EnableFileUpload, "enable-file-upload", false, "File upload support")
	flag.StringVar(&listen, "listen", ":4444", "Network address to accept connections")
	flag.IntVar(&config.RetryCount, "retry-count", 1, "New session attempts retry count")
	flag.DurationVar(&config.Timeout, "timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	flag.DurationVar(&config.MaxTimeout, "max-timeout", 1*time.Hour, "Maximum valid session idle timeout in time.Duration format")
	flag.DurationVar(&config.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.DurationVar(&config.ServiceStartupTimeout, "service-startup-timeout", 30*time.Second, "Service startup timeout in time.Duration format")
	flag.StringVar(&config.VideoRecorderImage, "video-recorder-image", "selenoid/video-recorder:latest-release", "Image to use as video recorder")
	flag.DurationVar(&gracefulPeriod, "graceful-period", 300*time.Second, "graceful shutdown period in time.Duration format, e.g. 300s or 500ms")
	// AWS Related args
	flag.StringVar(&config.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&config.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&config.AwsCluster, "aws-cluster", "esg", "AWS cluster name")
	flag.StringVar(&config.AwsElasticCache, "aws-elastic-cache", "localhost:6379", "AWS elastic cache connection URL")
	flag.StringVar(&config.AwsAutoScalingGroup, "aws-auto-scaling-group", "esg-asg", "AWS auto scaling group name")
	flag.IntVar(&config.MinMemory, "min-memory", 1024, "AWS minimum memory limitation for session")
	flag.IntVar(&config.MinMemoryReservation, "min-memory-reservation", 1024, "AWS minimum memory reservation limitation for session")
	flag.IntVar(&config.MaxMemory, "max-memory", 8192, "AWS maximum memory limitation for session")
	flag.IntVar(&config.MaxMemoryReservation, "max-memory-reservation", 8192, "AWS maximum memory reservation limitation for session")
	flag.IntVar(&config.MinCpu, "min-cpu", 1024, "AWS minimum CPU limitation for session")
	flag.IntVar(&config.MaxCpu, "max-cpu", 4096, "AWS maximum CPU limitation for session")
	flag.StringVar(&config.S3Bucket, "s3-bucket", "", "S3 Bucket name for pushing artifacts")
	flag.StringVar(&config.Tenant, "tenant", "", "Zebrunner tenant name")
	flag.StringVar(&config.AwsAccessKeyID, "aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&config.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for S3 bucket")
	flag.StringVar(&config.DbConnectionString, "db-connection", "", "Connection string for database")
	flag.BoolVar(&config.TrustedMode, "trusted", false, "If trusted mode enabled hub does not require any auth")
	flag.StringVar(&config.LogLevel, "log-level", "debug", "Desired log level. Valid levels: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, `trace`")

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
	r.Use(gin.LoggerWithFormatter(utils.TraceLogFromating), gin.Recovery())

	api := r.Group("/api")
	api.Use(handlers.APIError)
	{
		api.POST("/users", handlers.CreateUser)
		api.DELETE("/users/:username", handlers.DeleteUser)
		api.PUT("/users/:username/refresh-token", handlers.RefreshToken)
		api.PUT("/users/:username/activation", handlers.UserActivation)
		api.GET("/logs/:session", handlers.Logs)
		api.GET("/video/:session", handlers.Video)
	}

	hub := r.Group("/")
	hub.Use(handlers.SeleniumError)
	{
		hub.GET("/", handlers.Welcome)
		hub.GET("/status", handlers.Authentication, handlers.ClusterStatus)
		hub.GET("/ping", handlers.Ping)

		hub.Any("/wd/hub/*action", ReverseProxy())
		hub.POST("/session", handlers.Create) // Auth logic moved to handler
		hub.Any("/session/*action", handlers.Proxy)

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
		hub.DELETE("/download/:session/:file", handlers.Downloads)

		hub.GET("/clipboard/:session", handlers.Clipboard)
		hub.POST("/clipboard/:session", handlers.Clipboard)

		hub.GET("/devtools/:session", handlers.Devtools)
	}

	hub.Use(handlers.APIError)
	{
		hub.GET("/logs/:session", handlers.Logs)
		hub.GET("/video/:session", handlers.Video)
	}

	return r
}

func main() {
	log.SetLevel(config.ParseLogLevel())
	log.SetFormatter(&log.TextFormatter{
		FullTimestamp:   true,
		TimestampFormat: time.RFC3339Nano,
		ForceColors:     true,
	})

	db, err := service.InitDBConnection(config.DbConnectionString)
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
