package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/http/httputil"

	"github.com/gin-gonic/gin"
	"golang.org/x/net/websocket"

	//"github.com/zebrunner/esg/webserver"
	"log"

	"net/http"
	"os"
	"strings"
	"time"

	"path/filepath"

	"fmt"

	"github.com/zebrunner/esg/handlers"
	"github.com/zebrunner/esg/service"
)

var (
	listen             string
	gracefulPeriod     time.Duration
	retryCount         int
	dbConnectionString string

	version bool
)

func init() {
	flag.BoolVar(&handlers.EnableFileUpload, "enable-file-upload", false, "File upload support")
	flag.StringVar(&listen, "listen", ":4444", "Network address to accept connections")
	flag.IntVar(&retryCount, "retry-count", 1, "New session attempts retry count")
	flag.DurationVar(&handlers.Timeout, "timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	flag.DurationVar(&handlers.MaxTimeout, "max-timeout", 1*time.Hour, "Maximum valid session idle timeout in time.Duration format")
	//flag.DurationVar(&service.newSessionAttemptTimeout, "session-attempt-timeout", 30*time.Second, "New session attempt timeout in time.Duration format")
	flag.DurationVar(&handlers.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.DurationVar(&handlers.ServiceStartupTimeout, "service-startup-timeout", 30*time.Second, "Service startup timeout in time.Duration format")
	//flag.BoolVar(&version, "version", false, "Show version and exit")
	flag.BoolVar(&handlers.CaptureDriverLogs, "capture-driver-logs", false, "Whether to add driver process logs to Selenoid output")
	//flag.StringVar(&handlers.VideoOutputDir, "video-output-dir", "video", "Directory to save recorded video to")
	flag.StringVar(&handlers.VideoRecorderImage, "video-recorder-image", "selenoid/video-recorder:latest-release", "Image to use as video recorder")
	flag.StringVar(&handlers.LogOutputDir, "log-output-dir", "", "Directory to save session log to")
	flag.BoolVar(&handlers.SaveAllLogs, "save-all-logs", false, "Whether to save all logs without considering capabilities")
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

	var err error
	//hostname, err = os.Hostname()
	//if err != nil {
	//	log.Fatalf("[-] [INIT] [%s: %v]", os.Args[0], err)
	//}
	handlers.VideoOutputDir, err = filepath.Abs(handlers.VideoOutputDir)
	if err != nil {
		log.Fatalf("[-] [INIT] [Invalid video output dir %s: %v]", handlers.VideoOutputDir, err)
	}
	err = os.MkdirAll(handlers.VideoOutputDir, os.FileMode(0644))
	if err != nil {
		log.Fatalf("[-] [INIT] [Failed to create video output dir %s: %v]", handlers.VideoOutputDir, err)
	}
	log.Printf("[-] [INIT] [Video Dir: %s]", handlers.VideoOutputDir)

	if handlers.LogOutputDir != "" {
		handlers.LogOutputDir, err = filepath.Abs(handlers.LogOutputDir)
		if err != nil {
			log.Fatalf("[-] [INIT] [Invalid log output dir %s: %v]", handlers.LogOutputDir, err)
		}
		err = os.MkdirAll(handlers.LogOutputDir, os.FileMode(0644))
		if err != nil {
			log.Fatalf("[-] [INIT] [Failed to create log output dir %s: %v]", handlers.LogOutputDir, err)
		}
		log.Printf("[-] [INIT] [Logs Dir: %s]", handlers.LogOutputDir)
		if handlers.SaveAllLogs {
			log.Printf("[-] [INIT] [Saving all logs]")
		}
	}
}

//
//var paths = struct {
//	Video, VNC, Logs, Devtools, Download, Clipboard, File, Ping, Status, Error, WdHub, Welcome, Users string
//}{
//	VNC:       "/vnc/",
//	Devtools:  "/devtools/",
//	Download:  "/download/",
//	Clipboard: "/clipboard/",
//	File:      "/file",
//}

// TODO: Realize support for requests
//func handler() http.Handler {
//	root := http.NewServeMux()
//	root.HandleFunc(paths.Download, handlers.ReverseProxy(func(sess *session.Session) string { return sess.HostPort.Fileserver }, "DOWNLOADING_FILE"))
//	root.HandleFunc(paths.Clipboard, handlers.ReverseProxy(func(sess *session.Session) string { return sess.HostPort.Clipboard }, "CLIPBOARD"))
//	root.HandleFunc(paths.Devtools, handlers.ReverseProxy(func(sess *session.Session) string { return sess.HostPort.Devtools }, "DEVTOOLS"))
//	return root
//}

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
	r := gin.Default()

	api := r.Group("/api")
	api.Use(handlers.APIError)
	{
		api.POST("/users", handlers.CreateUser)
		api.DELETE("/users/:username", handlers.DeleteUser)
		api.PUT("/users/:username/refreshToken", handlers.RefreshToken)
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

		hub.Any("/file/:session", handlers.File)

		hub.GET("/download/:session/:file", handlers.Downloads)
		hub.DELETE("/download/:session/:file", handlers.Downloads)

		hub.GET("/clipboard/:session", handlers.Clipboard)
		hub.POST("/clipboard/:session", handlers.Clipboard)

		hub.GET("/devtools/:session", handlers.Devtools)
	}

	return r
}

func main() {
	log.Printf("[-] [INIT] [Timezone: %s]", time.Local)
	log.Printf("[-] [INIT] [Listening on %s]", listen)

	db, err := service.InitConnection(dbConnectionString)
	if err != nil {
		log.Printf("[-] [INIT] [Failed to start. Problem with db connection: %v]", err)
	}
	service.DB = db
	defer db.Close()

	rdb := service.InitCache()
	handlers.RDB = rdb
	defer rdb.Close()

	go func() {
		// TODO: Emulate session termination on selenium and try to return response
		// TODO: Move logic outside core ESG to run separately from main processes
		for {
			time.Sleep(handlers.Timeout)
			keys, err := rdb.Keys(context.Background(), "*").Result()
			if err != nil {
				log.Println("Error while getting list of keys", err)
				continue
			}

			for _, key := range keys {
				idle, err := rdb.ObjectIdleTime(context.Background(), key).Result()
				if err != nil {
					log.Printf("Error while getting IDLE time for session: %s. Error: %v", key, err)
					continue
				}

				if idle > handlers.Timeout {
					result, err := rdb.Get(context.Background(), key).Result()
					if err != nil {
						log.Printf("Error happened while getting session from cache. %v", err)
						continue
					}
					s := handlers.CachedSession{}
					err = json.Unmarshal([]byte(result), &s)
					if err != nil {
						log.Printf("Cant unmarshal redis data. Error: %v", err)
						continue
					}
					log.Printf("Deleting task: %s. Reason: idle timeout", s.TaskID)
					handlers.Delete(s.TaskID)
					_, err = rdb.Del(context.Background(), key).Result()
					if err != nil {
						log.Printf("can't delete session from redis cache. Session: %s. Error: %v", key, err)
					}
				}
			}
		}
	}()

	router := CreateRouter()
	err = router.Run(listen)
	if err != nil {
		log.Printf("[ERROR] Wrror while startup %v", err)
	}
}
