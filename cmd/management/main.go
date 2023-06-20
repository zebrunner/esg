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
	"github.com/zebrunner/esg/selenium"
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
		log.WithError(err).Error("Failed to create AWS session! Stopping scaler...")
		os.Exit(1)
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

		l := log.WithFields(log.Fields{"_taskId": session.TaskID, "sessionId": session.ID})
		if !config.Conf.SingleTenant {
			l = l.WithField("workspace", session.Workspace)
		}

		l.Debug("StopIdleTasks: analyzing session for idleTimeout")

		idleTimeout := float64(session.Capabilities.IdleTimeout)
		if idleTimeout == 0 {
			idleTimeout = config.Conf.IdleTimeout.Seconds()
		}

		idleTime := time.Since(session.AccessedAt).Seconds()
		if idleTime > idleTimeout {
			selenium.CloseSession(session, sessionmap.SessionIdleTimeout)
			_, err := service.StopTask(session.TaskID, sessionmap.SessionIdleTimeout)
			if err != nil {
				l.WithError(err).Error("Failed to stop idle driver task!")
			} else {
				l.Warn("task aborted due to the session idle timeout")
			}
		}
	}

	wg.Done()
}

func StopUnhealthyTasks(tasks []*ecs.Task, wg *sync.WaitGroup) {
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithField("_taskId", taskId)

		session, err := sessionmap.Find(taskId, false)
		if err != nil {
			continue
		}

		// stop zombie and UNHEALTHY tasks that are not pending for stop.
		// resource usage register and taskId mark for removal is performed only for stopped tasks
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			if *task.HealthStatus == "UNHEALTHY" {
				l.Warn("Aborting task due to UNHEALTHY HealthStatus")
				_, err := service.StopTask(taskId, sessionmap.SessionUnhealthy)
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
					_, err := service.StopTask(taskId, sessionmap.SessionMaxTimeout)
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
		log.WithError(err).Error("Error on ecs list-tasks operation")
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
		wg.Done()
		return
	}

	tasks, err := service.DescribeTasks(tasksToDescribe)
	if err != nil {
		wg.Done()
		return
	}

	for _, task := range tasks {
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			sessStartup := config.Conf.SessionStartupTimeout.Seconds()
			if task.CreatedAt != nil && time.Since(*task.CreatedAt).Seconds() > sessStartup {
				taskId := strings.Split(*task.TaskArn, "/")[2]
				l := log.WithField("_taskId", taskId)
				l.WithField("sessionStartupTimeout", sessStartup).Warn("Unrecognized task detected! Aborting")

				_, err := service.StopTask(taskId, sessionmap.SessionFinished)
				if err != nil {
					l.WithError(err).Error("Failed to stop the task")
				}
			}
		}
	}

	wg.Done()
}

func TrackResourceUsage(tasks []*ecs.Task, wg *sync.WaitGroup) {
	// analyze tasks response
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]

		// don't track tasks that:
		// 1) are not cached;
		// 2) already tracked;
		// 3) don't have the stop or generic status, because later we'll need a stop reason
		// 4) are not STOPPED
		session, err := sessionmap.Find(taskId, false)
		if err != nil ||
			session.UsageTracked ||
			(session.Status != sessionmap.SessionStopped && session.Status != sessionmap.SessionGeneric) ||
			*task.LastStatus != "STOPPED" {
			// TODO: delete session.Status != sessionmap.SessionGeneric when CloseSession() for generic tasks will be called
			continue
		}

		l := log.WithFields(log.Fields{"_taskId": taskId})
		if !config.Conf.SingleTenant {
			l = l.WithField("workspace", session.Workspace)
		}

		// track resources usage for STOPPED tasks

		// Set tracked status and expiration time 5 minutes to be able to return taskId and stop reason for task
		session.UsageTracked = true
		sessionmap.Write(taskId, session, 5*time.Minute)

		// Don't track Unhealthy and StartupFailure tasks
		if session.StopReason == sessionmap.SessionUnhealthy || session.StopReason == sessionmap.SessionStartupFailure {
			l.Info("Not tracking task with stop reason:", session.StopReason)
			continue
		}

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
	l := log.WithField("image", image)
	if err != nil {
		l.WithError(err).Error("Failed to build capabilities for image!")
		return err
	}

	env, err := environment.Build("", caps)
	if err != nil {
		l.WithError(err).Error("Failed to build execution environment!")
		return err
	}

	_, err = service.CreateTaskDefinition(env)
	if err != nil {
		l.WithError(err).Error("Failed to create task definition!")
		return err
	}

	return nil
}

func RefreshTaskDefinitions() {
	images := getImageList()

	for _, image := range images {
		l := log.WithField("image", image)
		err := RefreshTaskDefinition(image)
		if err != nil {
			l.WithError(err).Error("Couldn't create task defenition. Stopping scaler...")
			os.Exit(1)
		}
		time.Sleep(1 * time.Second)
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
		time.Sleep(12 * time.Hour)

		updatedImages := getImageList()
		for _, image := range updatedImages {
			if present := imagesSet[image]; !present {
				log.Info("Adding task definition for new image: ", image)
				err := RefreshTaskDefinition(image)
				if err == nil {
					imagesSet[image] = true
				} else {
					log.WithField("image", image).WithError(err).Error("Couldn't create task defenition. Stopping scaler...")
					os.Exit(1)
				}
			}
		}

	}
}

func refreshIMDSV2Token(){	
	for {
		err := utils.RefreshIMDSV2Token()
		if err != nil {
			log.Error(err)
		}
		time.Sleep(4 * time.Hour)
	}
}


func main() {
	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())

	awsSess, err := service.InitAws()
	if err != nil {
		log.WithError(err).Fatal("Failed to init aws session! Stopping scaler...")
		os.Exit(1)
	}
	service.AwsSess = awsSess

	rdb, err := config.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init redis connection! Stopping scaler...")
		os.Exit(1)
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

	go refreshIMDSV2Token()

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
