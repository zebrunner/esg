package main

import (
	"context"
	"flag"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	sessionmap "github.com/zebrunner/esg/sessinonmap"

	awsSession "github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	wg                  sync.WaitGroup
	enableFastScaleDown bool
)

func init() {
	flag.BoolVar(&enableFastScaleDown, "enable-fast-scale-down", true, "Enable ESG scale down option")
}

func ClearSessions() {
	rdb := config.RedisConnection
	for {
		time.Sleep(config.Conf.IdleTimeout)
		keys, err := rdb.Keys(context.Background(), "*").Result()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys")
			continue
		}

		for _, key := range keys {
			session, err := sessionmap.Find(key, false)
			if err != nil {
				log.WithError(err).WithField("session", key).Error("Failed to get session from session map")
				continue
			}

			if session.Status != sessionmap.SessionActive {
				continue
			}

			idleTimeout := float64(session.Capabilities.IdleTimeout)
			if idleTimeout == 0 {
				idleTimeout = config.Conf.IdleTimeout.Seconds()
			}

			idleTime := time.Since(session.AccessedAt).Seconds()
			if idleTime > idleTimeout {
				// Set stopped status and expiration time 10 minutes
				session.Status = sessionmap.SessionStoppedIdle
				err = sessionmap.Write(key, session, 10*time.Minute)

				log.WithField("task", session.TaskID).Info("Deleting task. Reason: idle timeout")
				selenium.CloseSession(session, &config.Conf)
				_, err = service.StopTask(session.TaskID)
				if err != nil {
					log.WithError(err).Error("Failed to stop task")
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

func ScaleDownCluster() {
	for {
		time.Sleep(30 * time.Second)
		service.ScaleDown()
	}
}

func RefreshTaskDefinition(image string) error {
	caps, err := capabilities.FromImage(image)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to build capabilities for image")
		return err
	}

	env, err := environment.Build("", caps, &config.Conf)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to build execution environment")
		return err
	}

	_, err = service.CreateTaskDefinition(env)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to create task definition")
		return err
	}

	return nil
}

func RefreshTaskDefinitions() {
	images, err := service.ListBrowsers()
	if err != nil {
		log.WithError(err).Error("Failed to get image list")
	}

	for _, image := range images {
		time.Sleep(1000 * time.Millisecond)
		err = RefreshTaskDefinition(image)
		if err != nil {
			continue
		}
	}
}

func RefreshTaskDefinitionsFromFile(path string) {
	text, err := ioutil.ReadFile(path)
	if err != nil {
		log.WithError(err).Error("Failed to read file browsers.txt")
	}
	lines := strings.Split(string(text), "\n")

	images := []string{}
	for _, line := range lines {
		if line != "" {
			images = append(images, line)
		}
	}

	log.WithField("images", images).Trace("Refreshing task definition using file")
	for _, image := range images {
		time.Sleep(1000 * time.Millisecond)
		err = RefreshTaskDefinition(image)
		if err != nil {
			continue
		}
	}
}


//TODO: move zombie tasks detection to service/wait.go and parametrize timeout
func CleanZombieTasks() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session")
		return
	}

	for {
		svc := ecs.New(session)
		tasks, err := service.GetClusterTasks(svc)
		if err != nil {
			log.WithError(err).Warn("Failed to get cluster tasks")
		}

		for _, task := range tasks {
			if time.Since(*task.CreatedAt) > 24*time.Hour {
				service.StopTask(*task.TaskArn)
			}
		}

		time.Sleep(1 * time.Hour)
	}
}

func main() {
	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())

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

	if config.Conf.BrowsersFile != "" {
		RefreshTaskDefinitionsFromFile(config.Conf.BrowsersFile)
	} else {
		RefreshTaskDefinitions()
	}
	log.Info("Task definitions refreshed successfully")

	wg.Add(1)
	go ScaleCluster()
	if enableFastScaleDown {
		wg.Add(1)
		go ScaleDownCluster()
	}
	wg.Add(1)
	go ClearSessions()

	wg.Add(1)
	go CleanZombieTasks()

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
