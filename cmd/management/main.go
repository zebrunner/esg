package main

import (
	"context"
	"encoding/json"
	"flag"
	"io/ioutil"
	"strings"
	"sync"
	"time"

	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	wg                  sync.WaitGroup
	browsersFile        string
	enableFastScaleDown bool
)

func init() {
	flag.StringVar(&browsersFile, "browsers-file", "", "Path to txt file with supported browsers")
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
			idle, err := rdb.ObjectIdleTime(context.Background(), key).Result()
			if err != nil {
				log.WithError(err).WithField("session", key).Error("Failed to get IDLE time for session.")
				continue
			}

			// Temporary solution. Session timeout saved separately with session.
			if strings.Contains(key, "timeout") {
				continue
			}
			sessionTimeout, err := rdb.Get(context.Background(), key+"-timeout").Int64()
			if err != nil {
				log.WithError(err).WithField("session", key).Error("Failed to get idle timeout")
			}

			timeout := config.Conf.IdleTimeout
			if sessionTimeout != 0 {
				timeout = time.Duration(sessionTimeout) * time.Second
			}
			if idle >= timeout {
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
				log.WithField("task", s.TaskID).Info("Deleting task. Reason: idle timeout")
				selenium.CloseSession(s.Workspace, key, &config.Conf)
				_, err = service.StopTask(s.TaskID)
				if err != nil {
					log.WithError(err).Error("Failed to stop task")
				}

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

func ScaleDownCluster() {
	for {
		time.Sleep(30 * time.Second)
		service.ScaleDown()
	}
}

func RefreshTaskDefinition(image string) error {
	caps, err := selenium.FromImage(image)
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
		log.WithError(err).WithField("image", image).Error("Failed to create task definitions")
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
	images, err := ioutil.ReadFile(path)
	if err != nil {
		log.WithError(err).Error("Failed to read file browsers.txt")
	}
	imageList := strings.Split(string(images), "\n")
	for _, image := range imageList {
		time.Sleep(1000 * time.Millisecond)
		err = RefreshTaskDefinition(image)
		if err != nil {
			continue
		}
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

	if browsersFile != "" {
		RefreshTaskDefinitionsFromFile(browsersFile)
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

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
