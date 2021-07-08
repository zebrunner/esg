package main

import (
	"context"
	"encoding/json"
	"flag"
	"github.com/zebrunner/esg/service"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/handlers"
)

var (
	wg sync.WaitGroup
)

func init() {
	flag.DurationVar(&config.Timeout, "timeout", 60*time.Second, "Session idle timeout in time.Duration format")
	// AWS Related args
	flag.StringVar(&config.AwsRegion, "aws-region", "us-east-1", "AWS region name")
	flag.IntVar(&config.AwsRetry, "aws-retry", 10, "AWS client retry count")
	flag.StringVar(&config.AwsCluster, "aws-cluster", "esg", "AWS cluster name")
	flag.StringVar(&config.AwsElasticCache, "aws-elastic-cache", "localhost:6379", "AWS elastic cache connection URL")
	flag.StringVar(&config.AwsAutoScalingGroup, "aws-auto-scaling-group", "esg-asg", "AWS auto scaling group name")
	flag.StringVar(&config.AwsAccessKeyID, "aws-access-key-id", "", "Access key for S3 bucket")
	flag.StringVar(&config.AwsSecretAccessKey, "aws-secret-access-key", "", "Secret key for S3 bucket")
	flag.StringVar(&config.LogLevel, "log-level", "debug", "Desired log level. Valid levels: `panic`, `fatal`, `error`, `warning`, `info`, `debug`, `trace`")

	flag.Parse()
}

func ClearSessions() {
	// TODO: Emulate session termination on selenium and try to return response
	// TODO: Move logic outside core ESG to run separately from main processes
	RDB, err := service.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init Redis client")
	}
	defer RDB.Close()

	for {
		time.Sleep(config.Timeout)
		keys, err := RDB.Keys(context.Background(), "*").Result()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys")
			continue
		}

		for _, key := range keys {
			idle, err := RDB.ObjectIdleTime(context.Background(), key).Result()
			if err != nil {
				log.WithError(err).WithField("session", key).Error("Failed to get IDLE time for session.")
				continue
			}

			if idle > config.Timeout {
				result, err := RDB.Get(context.Background(), key).Result()
				if err != nil {
					log.WithError(err).Error("Failed to get session from cache")
					continue
				}
				s := handlers.CachedSession{}
				err = json.Unmarshal([]byte(result), &s)
				if err != nil {
					log.WithError(err).Error("Failed to unmarshal redis response")
					continue
				}
				log.WithField("task", s.TaskID).Info("Deleting task. Reason: idle timeout")
				handlers.CloseSession(key)
				_, err = RDB.Del(context.Background(), key).Result()
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

func main() {
	log.SetLevel(config.ParseLogLevel())

	wg.Add(1)
	go ScaleCluster()
	wg.Add(1)
	go ClearSessions()

	wg.Wait()
	log.Panic("Background worker stopped!")
}
