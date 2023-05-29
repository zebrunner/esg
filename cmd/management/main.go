package main

import (
	"flag"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/service"
	sessionmap "github.com/zebrunner/esg/sessinonmap"
	"github.com/zebrunner/esg/utils"

	awsSession "github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

	"github.com/zebrunner/esg/zebrunner"
)

func ClearTasks() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session!")
		return
	}

	svc := ecs.New(session)
	var wg sync.WaitGroup

	for {
		time.Sleep(1 * time.Minute)

		keys, err := sessionmap.Keys()
		if err != nil {
			log.WithError(err).Error("Failed to get list of keys!")
			continue
		}

		if len(keys) > 0 {
			log.WithField("keys", keys).Trace("cached session keys")
		}

		tasks := service.GetSessionMapTasks(keys, svc)

		wg.Add(1)
		go StopIdleTasks(keys, &wg)

		wg.Add(1)
		go StopUnhealthyTasks(tasks, &wg)

		wg.Add(1)
		go StopLostTasks(keys, svc, &wg)

		wg.Add(1)
		go TrackResourceUsage(tasks, &wg)

		wg.Wait()
	}
}

func StopIdleTasks(keys []string, wg *sync.WaitGroup) {
	for _, key := range keys {
		session, _ := sessionmap.Find(key, false)

		if session == nil {
			log.WithField("session key", key).Debug("StopIdleTasks: Not found")
			continue
		}

		if session.Status != sessionmap.SessionActive {
			continue
		}

		log.WithField("session", session.TaskID).Debug("StopIdleTasks: analyzing session for idleTimeout")

		idleTimeout := float64(session.Capabilities.IdleTimeout)
		if idleTimeout == 0 {
			idleTimeout = config.Conf.IdleTimeout.Seconds()
		}

		idleTime := time.Since(session.AccessedAt).Seconds()
		if idleTime > idleTimeout {
			// [VD] do not execute CloseSession as it remove session from sessionmap and we can't return idle timeout errors to client
			//selenium.CloseSession(session)
			_, err := service.StopTask(session.TaskID)
			if err != nil {
				log.WithError(err).Error("Failed to stop idle driver task!")
			} else {
				// Set idle stopped status and expiration time 10 minutes to be able to return "invalid session id" for requests
				session.Status = sessionmap.SessionStoppedIdle
				sessionmap.Write(key, session, 10*time.Minute)
				log.WithField("_taskId", session.TaskID).WithField("workspace", session.Workspace).Warn("task aborted due to the idle timeout")
			}
		}
	}

	wg.Done()
}

func StopUnhealthyTasks(tasks []*ecs.Task, wg *sync.WaitGroup) {
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithFields(log.Fields{"_taskId": taskId})

		session, err := sessionmap.Find(taskId, false)
		if err != nil {
			continue
		}

		// stop zombie and UNHEALTHY tasks that are not pending for stop.
		// resource usage register and taskId mark for removal is performed only for stopped tasks
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			if *task.HealthStatus == "UNHEALTHY" {
				l.Warn("Aborting task due to UNHEALTHY HealthStatus")
				_, err := service.StopTask(taskId)
				if err != nil {
					l.WithError(err).Error("Failed to stop the task")
				}
			} else {
				maxTimeout := config.Conf.MaxTimeout
				if session.Capabilities.MaxTimeout != 0 {
					maxTimeout = time.Duration(session.Capabilities.MaxTimeout) * time.Second
				}
				l.Debug("maxTimeout: ", maxTimeout)

				if task.CreatedAt != nil && time.Since(*task.CreatedAt) > maxTimeout {
					l.WithField("maxTimeout", maxTimeout).Warn("Aborting task due to the max timeout")
					_, err := service.StopTask(taskId)
					if err != nil {
						l.WithError(err).Error("Failed to stop the task")
					}
				}
			}
		}
	}
	wg.Done()
}

func StopLostTasks(keys []string, svc *ecs.ECS, wg *sync.WaitGroup) {
	taskArns, err := service.GetClusterTasksArn(svc)
	if err != nil {
		log.WithError(err).Error("Error on ListTasks operation")
	}

	tasksToDescribe := make([]string, 0)

	for _, taskArn := range taskArns {
		isFound := false
		for _, key := range keys {
			taskId := strings.Split(*taskArn, "/")[2]
			if key == taskId {
				isFound = true
				break
			}
		}

		if !isFound {
			tasksToDescribe = append(tasksToDescribe, *taskArn)
		}
	}

	if len(tasksToDescribe) == 0 {
                // no need to print any log message because it should happen in 99.99% cases.
		return
	}
	maxRetryCount := 10

	var tasks []*ecs.Task
	for i := 1; i <= maxRetryCount; i++ {
		time.Sleep(time.Duration(i) * 1 * time.Second)
		tasks, err = service.DescribeTasks(tasksToDescribe)
		if err != nil {
			if i == maxRetryCount {
				log.WithField("error", err).Errorf("Couldn't DescribeTasks in %d retries.", i)
				return
			} else if strings.Contains(err.Error(), "ThrottlingException") {
				log.WithField("error", err).WithFields(log.Fields{"retry": i}).Debug("Recdescribing task defenition")
			} else {
				log.WithField("error", err).Error("Couldn't DescribeTasks.")
				continue
			}
		} else {
			break
		}
	}

	for _, task := range tasks {
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			sessStartup := config.Conf.SessionStartupTimeout.Seconds()
			if task.CreatedAt != nil && time.Since(*task.CreatedAt).Seconds() > sessStartup {
				log.WithField("sessionStartupTimeout", sessStartup).Warn("Task is running but wasn't cached in sessionMap in time. Aborting")
				taskId := strings.Split(*task.TaskArn, "/")[2]
				_, err := service.StopTask(taskId)
				if err != nil {
					log.WithError(err).WithField("taskId", taskId).Error("Failed to stop the task")
				}
			}
		}
	}

	wg.Done()
}

func TrackResourceUsage(tasks []*ecs.Task, wg *sync.WaitGroup) {
	// track STOPPED tasks id for removing them from sessionMap
	var taskIds4Removal []string

	// analyze tasks response
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithFields(log.Fields{"_taskId": taskId})

		session, err := sessionmap.Find(taskId, false)
		if err != nil {
			continue
		}

		// add task id for removal and track resources usage for STOPPED tasks
		if *task.LastStatus == "STOPPED" {
			l = l.WithFields(log.Fields{"workspace": session.Workspace})

			if task.StartedAt != nil && task.StoppedAt != nil {
				// don't calculate timing for terminated tasks by AWS due to the missted StartedAt!
				//	StopCode: \"TerminationNotice\"
				//	StoppedReason: \"Host EC2 (instance i-03dba81187d65ce7e) terminated.\"
				l.Trace("StartedAt: ", *task.StartedAt)
				l.Trace("StoppedAt: ", *task.StoppedAt)
				startedAt := *task.StartedAt //local var needed to calculate difference via Sub(..)
				stoppedAt := *task.StoppedAt
				zebrunner.TrackResourcesUsage(session, stoppedAt.Sub(startedAt))
			}

			if !strings.HasPrefix(session.Capabilities.Image, "public.ecr.aws/zebrunner/cypress-") {
				// #503: суpress tests aborted automatically
				// automatic abort of the public.ecr.aws/zebrunner/cypress-* should be prohibited as execution is control by parent cyserver process
				zebrunner.AbortTask(session, task)
			}
			taskIds4Removal = append(taskIds4Removal, taskId)
		}
	}

	// cleanup tracked task sessions
	for _, id := range taskIds4Removal {
		log.WithField("taskId", id).Trace("Removing task session")
		err := sessionmap.Remove(id)
		if err != nil {
			log.WithError(err).WithField("id", id).Error("Failed to remove task session from sessionmap!")
		}
	}
	wg.Done()
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
		log.WithError(err).WithField("image", image).Error("Failed to build capabilities for image!")
		return err
	}

	env, err := environment.Build("", caps)
	if err != nil {
		log.WithError(err).WithField("image", image).Error("Failed to build execution environment!")
		return err
	}

	_, err = service.CreateTaskDefinition(env)
	if err != nil {
		log.WithError(err).WithField("image", image).Debug("Failed to create task definition!")
		return err
	}

	return nil
}

func RefreshTaskDefinitions() {
	images := getImageList()

	for _, image := range images {
		maxRetryCount := 10
		for i := 1; i <= maxRetryCount; i++ {
			time.Sleep(time.Duration(i) * 1 * time.Second)
			err := RefreshTaskDefinition(image)
			if err != nil {
				if i == maxRetryCount {
					log.WithField("error", err).WithField("image", image).Errorf("Couldn't create task defenition in %d retries. Stopping scaler...", i)
					os.Exit(1)
				} else if strings.Contains(err.Error(), "ThrottlingException") {
					log.WithField("error", err).WithField("image", image).WithFields(log.Fields{"retry": i}).Debug("Recreating task defenition")
				} else {
					log.WithField("error", err).WithField("image", image).Error("Couldn't create task defenition. Stopping scaler...")
					os.Exit(1)
				}
			} else {
				break
			}
		}
	}
}

func getImageList() []string {
	images, err := utils.ListBrowsers()
	if err != nil {
		log.WithError(err).Error("Failed to get image list!")
		os.Exit(1)
	}

	return images
}

func getImageSet() map[string]bool {
	images := getImageList()

	imagesSet := make(map[string]bool, cap(images))
	for _, image := range images {
		imagesSet[image] = true
	}

	return imagesSet
}

func AddTaskDefinitions() {
	imagesSet := getImageSet()

	for {
		time.Sleep(24 * time.Hour)

		updatedImages := getImageList()
		for _, image := range updatedImages {
			if present := imagesSet[image]; !present {
				log.Info("Found new image in ecr: " + image)
				err := RefreshTaskDefinition(image)
				if err == nil {
					imagesSet[image] = true
				}
			}
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

	RefreshTaskDefinitions()
	log.Info("Task definitions updates finished")

	var wg sync.WaitGroup
	wg.Add(1)

	go ScaleCluster()

	go ScaleDownCluster()

	go ClearTasks()

	go AddTaskDefinitions()

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
