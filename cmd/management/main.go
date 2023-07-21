package main

import (
	"database/sql"
	"flag"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
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

		taskIds, err := taskmap.Keys()
		if err != nil {
			log.WithError(err).Error("Failed to get list of taskmap keys!")
			continue
		}

		if len(taskIds) > 0 {
			log.WithField("keys:", taskIds).Trace("cached task keys")
		}
		wg.Add(1)
		go StopLostTasks(taskIds, svc, &wg)

		tasks := service.GetTasksByTaskIds(taskIds, svc)
		if len(tasks) != 0 {
			wg.Add(1)
			go StopUnhealthyTasks(tasks, &wg)

			wg.Add(1)
			go TrackResourceUsage(tasks, &wg)
		}

		wg.Wait()
	}
}

func StopUnhealthyTasks(tasks []*ecs.Task, wg *sync.WaitGroup) {
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]

		cachedTask, err := taskmap.Find(taskId)
		if err != nil {
			log.WithError(err).Debug("StopUnhealthyTasks(): failed to get task's cache for: ", taskId)
			continue
		}

		l := log.WithField("_taskId", cachedTask.ID)

		// stop zombie and UNHEALTHY tasks that are not pending for stop.
		// resource usage register and taskId mark for removal is performed only for stopped tasks
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			if *task.HealthStatus == "UNHEALTHY" {
				l.Warn("Aborting task due to UNHEALTHY HealthStatus")
				err := service.StopTask(cachedTask.ID, taskmap.TaskUnhealthy)
				if err != nil {
					l.WithError(err).Error("Failed to stop the task")
				}
			} else {
				maxTimeout := config.Conf.MaxTimeout
				if cachedTask.Capabilities.MaxTimeout != 0 {
					maxTimeout = time.Duration(cachedTask.Capabilities.MaxTimeout) * time.Second
				}
				l.Trace("maxTimeout: ", maxTimeout)

				if task.CreatedAt != nil && time.Since(*task.CreatedAt) > maxTimeout {
					l.WithField("maxTimeout", maxTimeout).Warn("Aborting task due to the max timeout")
					err := service.StopTask(cachedTask.ID, taskmap.TaskMaxTimeout)
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
		log.WithError(err).Warn("StopLostTasks(): failed to describe lost tasks")
		wg.Done()
		return
	}

	for _, task := range tasks {
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			taskId := strings.Split(*task.TaskArn, "/")[2]
			l := log.WithField("_taskId", taskId)
			l.Warn("Unrecognized task detected! Aborting")

			cachedTask := &taskmap.Task{
				ID:     taskId,
				Status: taskmap.TaskActive,
			}
			// maybe we can track lost task's session and restore lost cache
			taskmap.Write(cachedTask.ID, cachedTask, 0)

			err := service.StopTask(taskId, taskmap.TaskLost)
			if err != nil {
				l.WithError(err).Error("Failed to stop the task")
			}
		}
	}

	wg.Done()
}

func TrackResourceUsage(tasks []*ecs.Task, wg *sync.WaitGroup) {
	// analyze tasks response
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithFields(log.Fields{"_taskId": taskId})

		// tracking task only when execution is stopped
		if *task.LastStatus != "STOPPED" {
			continue
		}

		// for tracking task should be cached
		cachedTask, _ := taskmap.Find(taskId)
		if cachedTask == nil {
			l.Debug("Can't find non tracked stopped task in cache")
			continue
		}

		// for tracking task should be:
		// 1) not tracked
		// 2) with stop or generic(workaround) status, because later we'll need a stop reason
		if cachedTask.UsageTracked || !(cachedTask.Status == taskmap.TaskStopped || cachedTask.Status == taskmap.TaskGeneric) {
			// TODO: delete cachedTask.Status != taskmap.TaskGeneric when CloseGeneric() for generic tasks will be called
			continue
		}

		if !config.Conf.SingleTenant {
			l = l.WithField("workspace", cachedTask.Workspace)
		}

		// track resources usage for STOPPED tasks
		// Set tracked status and expiration time 5 minutes to be able to return taskId and stop reason for task
		cachedTask.UsageTracked = true
		taskmap.Write(taskId, cachedTask, 5*time.Minute)

		// Don't track Unhealthy and StartupFailure tasks
		if cachedTask.StopReason == taskmap.TaskStartupFailure ||
			cachedTask.StopReason == taskmap.TaskUnhealthy ||
			cachedTask.StopReason == taskmap.TaskLost {
			l.Info("Not tracking task with stop reason:", cachedTask.StopReason)
			continue
		}

		zebrunner.TrackResourcesUsage(cachedTask, task)

		if !strings.HasPrefix(cachedTask.Capabilities.Image, "public.ecr.aws/zebrunner/cypress-") && cachedTask.Status == taskmap.TaskGeneric {
			// #503: суpress tests aborted automatically
			// automatic abort of the public.ecr.aws/zebrunner/cypress-* should be prohibited as execution is control by parent cyserver process
			zebrunner.AbortTask(cachedTask, task)
		}

	}
	wg.Done()
}

func StopIdleTasks() {
	for {
		time.Sleep(30 * time.Second)

		keys, err := sessionmap.Keys()
		if err != nil {
			log.WithError(err).Error("Failed to get list of sessionmap keys!")
			continue
		}

		if len(keys) > 0 {
			log.WithField("keys", keys).Trace("cached session keys")
		}

		for _, key := range keys {
			session, _ := sessionmap.Find(key, false)

			if session == nil {
				log.WithField("session key", key).Warn("StopIdleTasks: Not found")
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

			idleTime := time.Since(session.AccessedAt).Seconds()
			if idleTime > session.IdleTimeout {
				selenium.CloseSession(session, sessionmap.SessionIdleTimeout)
				err := service.StopTask(session.TaskID, taskmap.TaskAborted)
				if err != nil {
					l.WithError(err).Error("Failed to stop idle driver task!")
				} else {
					l.Warn("task aborted due to the session idle timeout")
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
	capsList, err := capabilities.FromImage(image)
	l := log.WithField("image", image)
	if err != nil {
		l.WithError(err).Error("Failed to build capabilities for image!")
		return err
	}

	for _, caps := range capsList {
		env, err := environment.Build("", caps)
		if err != nil {
			l.WithError(err).Error("Failed to build execution environment!")
			return err
		}

		l = l.WithField("schema", env.Schema).WithField("family", env.TaskDefinitionFamily)

		registerDefinitionHash := env.HashRegisterDefinition()
		dbDefinition, err := db.GetDefinition(env.TaskDefinitionFamily, env.Schema)
		if err != nil {
			if err == sql.ErrNoRows {

				hashPresent, tdToUpdate := db.IsHashPresent(registerDefinitionHash)
				if hashPresent {
					//situtation when task definition family naming were changed or/and changed containers naming(schema)
					if tdToUpdate.Family != env.TaskDefinitionFamily {
						err = db.UpdateTaskDefinitionFamily(tdToUpdate.Family, env.TaskDefinitionFamily)
						if err != nil {
							return err
						}
					}

					if tdToUpdate.Schema != env.Schema {
						err = db.UpdateSchema(tdToUpdate.Schema, env.Schema)
						if err != nil {
							return err
						}
					}

					err = definitionmap.AddDefinition(tdToUpdate.OverrideDefinitionHash, tdToUpdate.RevisionTag)
					if err != nil {
						return err
					}

					continue
				} else {

					l.Info("Creating new record")
					taskDef, err := service.CreateTaskDefinition(env)
					if err != nil {
						return err
					}
					// pause after aws call
					time.Sleep(1 * time.Second)

					newDefinition := &db.TaskDefinition{
						RevisionTag:            *taskDef.Revision,
						Family:                 env.TaskDefinitionFamily,
						Schema:                 env.Schema,
						RegisterDefinitionHash: registerDefinitionHash,
						OverrideDefinitionHash: env.HashOvverideDefinition(),
					}

					err = db.CreateDefinition(newDefinition)
					if err != nil {
						return err
					}

					err = definitionmap.AddDefinition(newDefinition.OverrideDefinitionHash, newDefinition.RevisionTag)
					if err != nil {
						return err
					}

					continue
				}
			} else {
				return err
			}
		}

		if dbDefinition.RegisterDefinitionHash != registerDefinitionHash {
			l.WithFields(log.Fields{"stored hash": dbDefinition.RegisterDefinitionHash, "new hash": registerDefinitionHash}).Info("Updating definition record")
			taskDef, err := service.CreateTaskDefinition(env)
			if err != nil {
				return err
			}
			// pause after aws call
			time.Sleep(1 * time.Second)

			updatedDefinition := &db.TaskDefinition{
				RevisionTag:            *taskDef.Revision,
				Family:                 env.TaskDefinitionFamily,
				Schema:                 env.Schema,
				RegisterDefinitionHash: registerDefinitionHash,
				OverrideDefinitionHash: env.HashOvverideDefinition(),
			}

			err = db.RefreshTag(dbDefinition.RegisterDefinitionHash, updatedDefinition)
			if err != nil {
				return err
			}

			err = definitionmap.AddDefinition(updatedDefinition.OverrideDefinitionHash, updatedDefinition.RevisionTag)
			if err != nil {
				return err
			}

			continue
		}

		l.Trace("Definition record is up-to-date")
		err = definitionmap.AddDefinition(dbDefinition.OverrideDefinitionHash, dbDefinition.RevisionTag)
		if err != nil {
			return err
		}

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
	}

	log.Info("Task definitions updates finished")
	definitionmap.SetRefreshDone()
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

func refreshIMDSV2Token() {
	for {
		err := utils.RefreshIMDSV2Token()
		if err != nil {
			log.WithError(err).Error("Failed to generate IMDSV2 token")
		} else {
			log.Debug("Successfully generated IMDSV2 token")
		}
		time.Sleep(2*time.Hour + 30*time.Minute)
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

	err = config.InitDBConnection(config.Conf.DbConnectionString)
	if err != nil {
		log.WithError(err).Fatal("Failed to init DB client! Stopping router...")
		os.Exit(1)
	}

	defer config.DbConnection.Close()

	err = config.InitCache()
	if err != nil {
		log.WithError(err).Fatal("Failed to init redis connection! Stopping scaler...")
		os.Exit(1)
	}

	defer config.RedisSessionsConnection.Close()
	defer config.RedisTasksConnection.Close()
	defer config.RedisDefinitionConnection.Close()

	RefreshTaskDefinitions()

	var wg sync.WaitGroup
	wg.Add(1)

	go ScaleCluster()

	go ScaleDownCluster()

	go ClearTasks()

	go StopIdleTasks()

	go AddTaskDefinitions()

	if config.Conf.Imdsv2Enabled {
		go refreshIMDSV2Token()
	}

	wg.Wait()
	log.Fatal("Background worker stopped!")
}
