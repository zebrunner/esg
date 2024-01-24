package main

import (
	"database/sql"
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/cachemaps/mapper"
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

func ClearTasks(shapingCh chan<- interface{}) {
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
		} else {
			continue
		}

		wg.Add(1)
		go StopLostTasks(taskIds, svc, &wg)

		tasks := service.GetTasksByTaskIds(taskIds, svc)
		if len(tasks) != 0 {
			cachedTasks, err := taskmap.Tasks(taskIds)
			if err != nil {
				log.Warn("Failed to get cached tasks")
				continue
			}

			cachedTasksMap := make(map[string]taskmap.Task, len(cachedTasks))
			for _, cachedTask := range cachedTasks {
				cachedTasksMap[cachedTask.TaskId] = cachedTask
			}

			wg.Add(1)
			go StopUnhealthyTasks(tasks, cachedTasksMap, &wg)

			wg.Add(1)
			go TrackResourceUsage(tasks, cachedTasksMap, &wg, shapingCh)
		}

		wg.Wait()
	}
}

func StopUnhealthyTasks(tasks []*ecs.Task, cachedTasksMap map[string]taskmap.Task, wg *sync.WaitGroup) {
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithField(config.TaskIdKey, taskId)
		// stop zombie and UNHEALTHY tasks that are not pending for stop.
		// resource usage register and taskId mark for removal is performed only for stopped tasks
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			cachedTask, ok := cachedTasksMap[taskId]
			if !ok {
				l.Warn("Failed to find task in cache")
				continue
			}

			if *task.HealthStatus == "UNHEALTHY" {
				l.Warn("Aborting task due to UNHEALTHY HealthStatus")
				err := service.StopTask(cachedTask, taskmap.TaskUnhealthy)
				if err != nil {
					l.WithError(err).Error("Failed to stop the task")
				}

				if cachedTask.Status == taskmap.TaskGeneric {
					zebrunner.AbortLaunch(cachedTask.RouterUUID, cachedTask.Workspace,
						cachedTask.Capabilities.LaunchUUID.ToPrimitive(), "Task aborted due to UNHEALTHY HealthStatus")
				}
			} else {
				maxTimeout := time.Duration(cachedTask.Capabilities.MaxTimeout) * time.Second
				if task.CreatedAt != nil && time.Since(*task.CreatedAt) > maxTimeout {
					l.WithField("maxTimeout", maxTimeout).Warn("Aborting task due to the max timeout")
					err := service.StopTask(cachedTask, taskmap.TaskMaxTimeout)
					if err != nil {
						l.WithError(err).Error("Failed to stop task. Trying to stop forcibly")
						err := service.StopTaskForcibly(cachedTask.TaskId, taskmap.TaskMaxTimeout)
						if err != nil {
							l.WithError(err).Error("Failed to stop task forcibly")
						}
					}

					if cachedTask.Status == taskmap.TaskGeneric {
						zebrunner.AbortLaunch(cachedTask.RouterUUID, cachedTask.Workspace,
							cachedTask.Capabilities.LaunchUUID.ToPrimitive(), "Task aborted due to the max timeout limit")
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
			if time.Since(*task.StartedAt) <= config.Conf.TaskUncachedTimeout {
				continue
			}
			taskId := strings.Split(*task.TaskArn, "/")[2]
			l := log.WithField(config.TaskIdKey, taskId)
			l.Warn("Unrecognized task detected! Aborting")

			err := service.StopTaskForcibly(taskId, taskmap.TaskLost)
			if err != nil {
				l.WithError(err).Error("Failed to stop the task")
			}
		}
	}

	wg.Done()
}

func TrackResourceUsage(tasks []*ecs.Task, cachedTasksMap map[string]taskmap.Task, wg *sync.WaitGroup, shapingCh chan<- interface{}) {
	// analyze tasks response
	tasksCacheToUpdate := make([]taskmap.Task, 0)
	tasksToTrack := make(map[*taskmap.Task]*ecs.Task)
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithField(config.TaskIdKey, taskId)

		// tracking task only when execution is stopped
		if *task.LastStatus != "STOPPED" {
			continue
		}

		// for tracking task should be cached
		cachedTask, ok := cachedTasksMap[taskId]
		if !ok {
			l.Debug("Can't find non tracked stopped task in cache")
			continue
		}

		// already tracked
		if cachedTask.UsageTracked {
			continue
		}

		if cachedTask.Status != taskmap.TaskStopped {
			// generic and cypress on success finish are not marked as stopped in cache after finish
			if cachedTask.Status == taskmap.TaskGeneric {
				cachedTask.Status = taskmap.TaskStopped
				cachedTask.StopReason = taskmap.TaskFinished
			} else {
				continue
			}
		}

		// track resources usage for STOPPED tasks
		cachedTask.UsageTracked = true
		tasksCacheToUpdate = append(tasksCacheToUpdate, cachedTask)

		if !config.Conf.SingleTenant {
			l = l.WithField("workspace", cachedTask.Workspace)
		}

		// Don't track Unhealthy/StartupFailure/Lost tasks
		if cachedTask.StopReason == taskmap.TaskStartupFailure ||
			cachedTask.StopReason == taskmap.TaskUnhealthy ||
			cachedTask.StopReason == taskmap.TaskLost {
			l.Info("Not tracking task with stop reason:", cachedTask.StopReason)
			continue
		}

		tasksToTrack[&cachedTask] = task
	}

	// Set tracked status and expiration time 5 minutes to be able to return taskId and stop reason for task
	err := taskmap.WriteAll(tasksCacheToUpdate, 5*time.Minute)
	if err != nil {
		log.WithError(err).Error("Failed to update tracked tasks!")
	} else {
		for cachedTask, task := range tasksToTrack {
			zebrunner.TrackResourcesUsage(cachedTask, task)
		}
	}

	select {
	case shapingCh <- "done":
	default:
	}

	wg.Done()
}

func StopIdleTasks() {
	isSessionIdle := func(sess sessionmap.Session) bool {
		if sess.Status != sessionmap.SessionActive {
			return false
		}

		idleTime := time.Since(sess.AccessedAt).Seconds()
		return idleTime > sess.IdleTimeout
	}

	for {
		time.Sleep(1 * time.Minute)

		Sessions, err := sessionmap.Sessions()
		if err != nil {
			log.WithError(err).Error("Failed to get list of sessionmap keys!")
			continue
		}

		if len(Sessions) > 0 {
			log.WithField("sessions", Sessions).Trace("cached sessions")
		}

		for _, session := range Sessions {
			timedOut := isSessionIdle(session)

			if timedOut {
				l := log.WithFields(log.Fields{config.TaskIdKey: session.TaskId, config.SessionIdKey: session.SessionID})
				if !config.Conf.SingleTenant {
					l = l.WithField("workspace", session.Workspace)
				}

				// get actual record of the session and validate idle timeout one more time
				sess, err := sessionmap.Find(session.SessionID, false)
				if err != nil {
					continue
				}

				timedOut = isSessionIdle(*sess)
				if timedOut {
					selenium.CloseSession(&session, sessionmap.SessionIdleTimeout)
					cachedTask, err := taskmap.Find(session.TaskId, false)
					if err != nil {
						l.WithError(err).Error("Failed to find cached task with idle session!")
						continue
					}

					err = service.StopTask(*cachedTask, taskmap.TaskAborted)
					if err != nil {
						l.WithError(err).Error("Failed to stop idle driver task!")
					} else {
						l.Warn("task aborted due to the session idle timeout")
					}
				}
			}
		}
	}
}

func StopCypressIdleTasks() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session! Stopping scaler...")
		os.Exit(1)
	}

	svc := ecs.New(session)
	for {
		time.Sleep(1 * time.Minute)

		keys, err := taskmap.CypressSetKeys()
		if err != nil {
			log.WithError(err).Error("Failed to get set of cypress keys!")
			continue
		}

		if len(keys) == 0 {
			continue
		}

		cachedTasks, err := taskmap.Tasks(keys)
		if err != nil {
			log.WithError(err).Error("Failed to get tasks from cypress keys!")
			continue
		}

		tasksToStop := make([]string, 0)
		cachedTasksMap := make(map[string]taskmap.Task)
		for _, cachedTask := range cachedTasks {
			l := log.WithField(config.TaskIdKey, cachedTask.TaskId)
			idleTime := time.Since(cachedTask.AccessedAt).Seconds()
			if idleTime > config.Conf.CypressIdleTimeout.Seconds() {
				l.Debug("StopCypressIdleTasks: analyzing task for idleTimeout")
				tasksToStop = append(tasksToStop, cachedTask.TaskId)
				cachedTasksMap[cachedTask.TaskId] = cachedTask
			}
		}

		if len(tasksToStop) == 0 {
			continue
		}

		tasks := service.GetTasksByTaskIds(tasksToStop, svc)
		for _, task := range tasks {
			taskId := strings.Split(*task.TaskArn, "/")[2]
			l := log.WithField(config.TaskIdKey, taskId)

			if *task.LastStatus == "STOPPED" || *task.DesiredStatus == "STOPPED" {
				taskmap.RemoveFromCypressSet(taskId)
			} else {
				err := service.StopTask(cachedTasksMap[taskId], taskmap.TaskAborted)
				if err != nil {
					l.WithError(err).Error("Failed to stop cypress idle task!")
					continue
				}

				taskmap.RemoveFromCypressSet(taskId)
				l.Warn("cypress task aborted due to the idle timeout")
			}
		}
	}
}

func RefreshTaskDefinition(env *environment.ExecutionEnvironment) (*db.TaskDefinition, error) {
	l := log.WithField("schema", env.Schema).WithField("family", env.TaskDefinitionFamily)

	newDbDefinititon := db.CreateTaskDefinitionEntity(env)
	savedDbDefinition, err := db.GetDefinition(env.TaskDefinitionFamily, env.Schema)
	if err != nil {
		if err != sql.ErrNoRows {
			return nil, err
		}

		l.Info("Creating new record")
		taskDef, err := service.CreateTaskDefinition(env)
		if err != nil {
			return nil, err
		}
		// pause after aws call
		time.Sleep(1 * time.Second)
		newDbDefinititon.RevisionTag = *taskDef.Revision

		err = db.InsertDefinition(newDbDefinititon)
		if err != nil {
			return nil, err
		}
	} else if newDbDefinititon.RegisterDefinitionHash != savedDbDefinition.RegisterDefinitionHash {
		l.Info("Updating definition record")
		taskDef, err := service.CreateTaskDefinition(env)
		if err != nil {
			return nil, err
		}
		// pause after aws call
		time.Sleep(1 * time.Second)
		newDbDefinititon.RevisionTag = *taskDef.Revision

		err = db.RefreshTag(savedDbDefinition.RegisterDefinitionHash, newDbDefinititon)
		if err != nil {
			return nil, err
		}
	} else {
		l.Trace("Definition record is up-to-date")
		newDbDefinititon.RevisionTag = savedDbDefinition.RevisionTag
	}

	return newDbDefinititon, nil
}

func RefreshTaskDefinitions() {
	refreshInterval := time.Hour * 12
	for {
		images, err := utils.ListImages()
		if err != nil {
			log.WithError(err).Error("Failed to get images list!")
			os.Exit(1)
		}

		envsList, err := BuildEnvsFromImages(images)
		if err != nil {
			log.WithError(err).Error("Failed to build execution environments from images list!")
			os.Exit(1)
		}

		hashRevisionMap := make(map[string]int64)
		for _, env := range envsList {
			dbTaskDefinition, err := RefreshTaskDefinition(env)
			if err != nil {
				log.WithField("family", env.TaskDefinitionFamily).WithError(err).Error("Couldn't create task defenition. Stopping scaler...")
				os.Exit(1)
			}

			hashRevisionMap[dbTaskDefinition.OverrideDefinitionHash] = dbTaskDefinition.RevisionTag
		}

		err = definitionmap.WriteAll(hashRevisionMap, refreshInterval+time.Hour)
		if err != nil {
			log.WithError(err).Error("Failed to add hashRevision map to redis. Stopping scaler...")
			os.Exit(1)
		}

		log.Info("Task definitions update finished")
		definitionmap.SetRefreshDone()
		time.Sleep(refreshInterval)
	}
}

func BuildEnvsFromImages(images []string) ([]*environment.ExecutionEnvironment, error) {
	envsList := make([]*environment.ExecutionEnvironment, 0)
	for _, image := range images {
		l := log.WithField("image", image)

		capsList, err := capabilities.FromImage(image)
		if err != nil {
			l.WithError(err).Error("Failed to build capabilitites from image!")
			return nil, err
		}

		for _, caps := range capsList {
			env, err := environment.BuildFromCaps(caps)
			if err != nil {
				l.WithError(err).Error("Failed to build execution environment from capabilities!")
				return nil, err
			}

			envsList = append(envsList, env)
		}
	}

	return envsList, nil
}

func refreshIMDSV2Token() {
	for {
		err := utils.RefreshIMDSV2Token()
		if err != nil {
			log.WithError(err).Error("Failed to generate IMDSV2 token. Stopping scaler...")
			os.Exit(1)
		}

		log.Debug("Successfully generated IMDSV2 token")
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

	defer config.RedisSessionsClient.Close()
	defer config.RedisTasksClient.Close()
	defer config.RedisDefinitionClient.Close()
	defer config.RedisCypressSetClient.Close()
	defer config.RedisIdMapperClient.Close()
	defer config.RedisResourcesClient.Close()
	mapper.InitUUIDMapWorkers()
	taskmap.InitTaskmapWorkers()
	sessionmap.InitSessionmapWorker()
	// scaler don't need ResourceWorker
	// resourcesToAllocate.InitResourceWorker()

	service.InitScalingData()
	service.StartScalers()

	go RefreshTaskDefinitions()

	shapingCh := make(chan interface{})
	go ClearTasks(shapingCh)

	go StopIdleTasks()

	go StopCypressIdleTasks()

	go refreshIMDSV2Token()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	log.Info("Shutdown scaler ...")

	// on shutdown actions
	err = definitionmap.Remove(definitionmap.TaskDefenititonRefreshDone)
	if err != nil {
		log.WithError(err).Error("Failed to unmark task definition refresh")
	}

	// wait for the end of a resources shaping
	log.Info("Waiting for shaping...")
	<-shapingCh
	log.Info("Shaping performed")
}
