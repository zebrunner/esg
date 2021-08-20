package main

import (
	"context"
	"encoding/json"
	"flag"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	wg           sync.WaitGroup
	browsersFile string
)

func init() {
	//rename to idle timeout
	flag.DurationVar(&config.IdleTimeout, "idle-timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	// AWS Related args
	flag.StringVar(&config.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&config.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&config.AwsCluster, "aws-cluster", "esg", "AWS cluster name")
	flag.StringVar(&config.AwsElasticCache, "aws-elastic-cache", "localhost:6379", "AWS elastic cache connection URL")
	flag.StringVar(&config.AwsAutoScalingGroup, "aws-auto-scaling-group", "esg-asg", "AWS auto scaling group name")
	flag.StringVar(&config.AwsAccessKeyID, "aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&config.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for S3 bucket")
	flag.StringVar(&config.LogLevel, "log-level", "debug", "Desired log level. Valid levels: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, `trace`")
	flag.DurationVar(&config.SessionDeleteTimeout, "session-delete-timeout", 30*time.Second, "Session delete timeout in time.Duration format")
	flag.StringVar(&browsersFile, "browsers-file", "", "Path to txt file with supported browsers")

	flag.IntVar(&config.MinMemory, "min-memory", 1024, "AWS minimum memory limitation for session")
	flag.IntVar(&config.MinMemoryReservation, "min-memory-reservation", 1024, "AWS minimum memory reservation limitation for session")
	flag.IntVar(&config.MaxMemory, "max-memory", 8192, "AWS maximum memory limitation for session")
	flag.IntVar(&config.MaxMemoryReservation, "max-memory-reservation", 8192, "AWS maximum memory reservation limitation for session")
	flag.IntVar(&config.MinCpu, "min-cpu", 1024, "AWS minimum CPU limitation for session")
	flag.IntVar(&config.MaxCpu, "max-cpu", 4096, "AWS maximum CPU limitation for session")

	flag.StringVar(&config.ZebrunnerHost, "zebrunner-host", "", "Host for zebrunner integration for this environment")
	flag.StringVar(&config.ZebrunnerIntegrationUser, "zebrunner-integration-user", "", "User for zebrunner for current env")
	flag.StringVar(&config.ZebrunnerIntegrationPassword, "zebrunner-integration-password", "", "Password for zebrunner for current env")

	flag.Parse()
}

func ClearSessions() {
	rdb := config.RedisConnection
	for {
		time.Sleep(config.IdleTimeout)
		keys, err := rdb.Keys(context.Background(), "*").Result()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys")
			continue
		}

		for _, key := range keys {
			idle, err := rdb.ObjectIdleTime(context.Background(), key).Result()
			if err != nil {
				log.WithError(err).WithField("session", key).Error("Failed to get IDLE time for session.")
				continue
			}

			result, err := rdb.Get(context.Background(), key).Result()
			if err != nil {
				log.WithError(err).Error("Failed to get session from cache")
				continue
			}

			s := selenium.CachedSession{}
			err = json.Unmarshal([]byte(result), &s)
			if err != nil {
				log.WithError(err).Error("Failed to unmarshal redis response")
				continue
			}

			timeout := config.IdleTimeout
			if s.Conf.IdleTimeout != 0 {
				timeout = time.Duration(s.Conf.IdleTimeout) * time.Second
			}
			if idle > timeout {
				log.WithField("task", s.TaskID).Info("Deleting task. Reason: idle timeout")
				selenium.CloseSession(s.Workspace, key)
				_, err = rdb.Del(context.Background(), key).Result()
				if err != nil {
					log.WithError(err).WithField("session", key).Error("Failed to delete session from cache")
				}
			}
		}
	}
}

func ScaleCluster() {
	for {
		time.Sleep(10 * time.Second)
		service.ScaleUp()
	}
}

func RefreshTaskDefinitions() {
	images, err := service.ListBrowsers()
	if err != nil {
		log.WithError(err).Error("Failed to get image list")
	}

	for _, image := range images {
		family := strings.ReplaceAll(image, ":", "-")
		family = strings.ReplaceAll(family, ".", "-")

		_, err = service.CreateTaskDefinition(image, family)
		if err != nil {
			log.WithError(err).WithField("family", family).Error("Failed to create task definitions")
		}

		time.Sleep(250 * time.Millisecond)
	}
}

func RefreshTaskDefinitionsFromFile(path string) {
	images, err := ioutil.ReadFile(path)
	if err != nil {
		log.WithError(err).Error("Failed to read file browsers.txt")
	}
	imageList := strings.Split(string(images), "\n")
	for _, image := range imageList {
		family := strings.ReplaceAll(image, ":", "-")
		family = strings.ReplaceAll(family, ".", "-")

		_, err = service.CreateTaskDefinition(image, family)
		if err != nil {
			log.WithError(err).WithField("family", family).Error("Failed to create task definitions")
		}

		time.Sleep(250 * time.Millisecond)
	}
}

func main() {
	log.SetLevel(config.ParseLogLevel())

	awsSess, err := service.InitAws()
	if err != nil {
		log.WithError(err).Fatal("Failed to init aws session")
	}
	service.AwsSess = awsSess

	rdb, err := config.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init redis connection")
	}
	config.RedisConnection = rdb

	if browsersFile != "" {
		RefreshTaskDefinitionsFromFile(browsersFile)
	} else {
		RefreshTaskDefinitions()
	}
	log.Info("Task definitions refreshed successfully")

	wg.Add(1)
	go ScaleCluster()
	wg.Add(1)
	go ClearSessions()

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
