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

func ManageTasksAndSession(iterationCh chan<- interface{}) {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		utils.ExitWithError(err, "Failed to create AWS session", log.NewEntry(log.StandardLogger()))
	}

	svc := ecs.New(session)
	var wg sync.WaitGroup

	for {
		time.Sleep(1 * time.Minute)

		wg.Add(1)
		go StopIdleTasks(&wg)

		LaunchTasksProcessors(svc, &wg)

		wg.Wait()
		select {
		case iterationCh <- "done":
		default:
		}
	}
}

func LaunchTasksProcessors(svc *ecs.ECS, wg *sync.WaitGroup) {
	routerUuids, err := mapper.GetKeys(mapper.TASK)
	if err != nil {
		log.WithError(err).Warn("Failed to get list of taskmap keys!")
		return
	}

	mapperEntities, err := mapper.FindAll(routerUuids)
	if err != nil {
		log.WithError(err).Warn("Failed to get cached mapper enties")
		return
	}

	taskIds := make([]string, 0)
	for _, mapperEntity := range mapperEntities {
		if mapperEntity.TaskId != "" {
			taskIds = append(taskIds, mapperEntity.TaskId)
		}
	}

	for i := 0; i < len(mapperEntities); i++ {
		taskIds[i] = mapperEntities[i].TaskId
	}

	wg.Add(1)
	go StopLostTasks(taskIds, svc, wg)

	if len(routerUuids) <= 0 {
		return
	}

	tasks := service.GetTasksByTaskIds(taskIds, svc)

	if len(tasks) <= 0 {
		return
	}

	taskIdMapperMap := make(map[string]mapper.Mapper)
	for _, mapperEntity := range mapperEntities {
		if mapperEntity.TaskId != "" {
			taskIdMapperMap[mapperEntity.TaskId] = mapperEntity
		}
	}

	wg.Add(1)
	go StopUnhealthyTasks(tasks, taskIdMapperMap, wg)

	wg.Add(1)
	go TrackResourceUsage(tasks, taskIdMapperMap, wg)
}

func StopUnhealthyTasks(tasks []*ecs.Task, cachedTasksMap map[string]mapper.Mapper, wg *sync.WaitGroup) {
	for _, task := range tasks {
		taskId := strings.Split(*task.TaskArn, "/")[2]
		l := log.WithField(config.TaskIdKey, taskId)
		// stop zombie and UNHEALTHY tasks that are not pending for stop.
		// resource usage register and taskId mark for removal is performed only for stopped tasks
		if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
			mapperEntity, ok := cachedTasksMap[taskId]
			if !ok {
				l.Warn("Failed to find task in cache")
				continue
			}

			if mapperEntity.Status == mapper.Queued {
				continue
			}

			if *task.HealthStatus == "UNHEALTHY" {
				l.Warn("Aborting task due to UNHEALTHY HealthStatus")
				err := service.StopTask(mapperEntity, mapper.TaskUnhealthy)
				if err != nil {
					l.WithError(err).Error("Failed to stop the task")
				}
			} else {
				maxTimeout := time.Duration(mapperEntity.Capabilities.MaxTimeout) * time.Second
				if task.CreatedAt != nil && time.Since(*task.CreatedAt) > maxTimeout {
					l.WithField("maxTimeout", maxTimeout).Warn("Aborting task due to the max timeout")
					err := service.StopTask(mapperEntity, mapper.TaskMaxTimeout)
					if err != nil {
						l.WithError(err).Error("Failed to stop task. Trying to stop forcibly")
						err := service.StopTaskForcibly(mapperEntity.TaskId, mapper.TaskMaxTimeout)
						if err != nil {
							l.WithError(err).Error("Failed to stop task forcibly")
						}
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

			err := service.StopTaskForcibly(taskId, mapper.TaskLost)
			if err != nil {
				l.WithError(err).Error("Failed to stop the task")
			}
		}
	}

	wg.Done()
}

func TrackResourceUsage(tasks []*ecs.Task, cachedTasksMap map[string]mapper.Mapper, wg *sync.WaitGroup) {
	// analyze tasks response
	tasksCacheToUpdate := make([]mapper.Mapper, 0)
	tasksToTrack := make(map[*mapper.Mapper]*ecs.Task)
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

		if cachedTask.Status != mapper.Stopped {
			// cypress is not marked as stopped in cache after finish
			if cachedTask.Status == mapper.Cypress {
				cachedTask.Status = mapper.Stopped
				cachedTask.StopReason = mapper.TaskFinished
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
		if cachedTask.StopReason == mapper.TaskStartupFailure ||
			cachedTask.StopReason == mapper.TaskUnhealthy ||
			cachedTask.StopReason == mapper.TaskLost {
			l.Info("Not tracking task with stop reason:", cachedTask.StopReason)
			continue
		}

		tasksToTrack[&cachedTask] = task
	}

	// Set tracked status and expiration time 5 minutes to be able to return taskId and stop reason for task
	err := mapper.WriteShapedEntities(tasksCacheToUpdate, 5*time.Minute)
	if err != nil {
		log.WithError(err).Error("Failed to update tracked tasks!")
	} else {
		for cachedTask, task := range tasksToTrack {
			zebrunner.TrackResourcesUsage(cachedTask, task)
		}
	}

	wg.Done()
}

func StopIdleTasks(wg *sync.WaitGroup) {
	routerUuids, err := mapper.GetKeys(mapper.SESSION)
	if err != nil {
		log.WithError(err).Error("Failed to list uuid keys from sessions set!")
		wg.Done()
		return
	}

	mapperEntities, err := mapper.FindAll(routerUuids)
	if err != nil {
		log.WithError(err).Error("Failed to get mapper entities!")
		wg.Done()
		return
	}

	for _, mapperEntity := range mapperEntities {
		idle := mapperEntity.IsIdle()

		if !idle {
			continue
		}

		l := log.WithFields(log.Fields{config.TaskIdKey: mapperEntity.TaskId, config.SessionIdKey: mapperEntity.SessionID, config.RouterUUID: mapperEntity.RouterUUID})
		if !config.Conf.SingleTenant {
			l = l.WithField("workspace", mapperEntity.Workspace)
		}

		// get actual record of the session and validate idle timeout one more time
		mapperEntity, err := mapper.Find(mapperEntity.SessionID, false)
		if err != nil {
			continue
		}

		idle = mapperEntity.IsIdle()

		if !idle {
			continue
		}

		wg.Add(1)
		go func(m *mapper.Mapper, l *log.Entry, wg *sync.WaitGroup) {
			selenium.CloseSession(m)

			err = service.StopTask(*m, mapper.SessionIdleTimeout)
			if err != nil {
				l.WithError(err).Error("Failed to stop idle driver task!")
			} else {
				l.Warn("task aborted due to the session idle timeout")
			}
			wg.Done()
		}(mapperEntity, l, wg)
	}

	wg.Done()
}

func refreshTaskDefinition(env *environment.ExecutionEnvironment) (*db.TaskDefinition, error) {
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

func buildEnvsFromImages(images []string) ([]*environment.ExecutionEnvironment, error) {
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

func refreshTaskDefinitions(taskDefinitionCacheTtl time.Duration) error {
	images, err := utils.ListImages()
	if err != nil {
		log.WithError(err).Error("Failed to get images list")
		return err
	}

	envsList, err := buildEnvsFromImages(images)
	if err != nil {
		log.WithError(err).Error("Failed to build execution environments from images list")
		return err
	}

	hashRevisionMap := make(map[string]int64)
	for _, env := range envsList {
		dbTaskDefinition, err := refreshTaskDefinition(env)
		if err != nil {
			log.WithError(err).WithField("family", env.TaskDefinitionFamily).Error("Couldn't create task defenition")
			return err
		}

		hashRevisionMap[dbTaskDefinition.OverrideDefinitionHash] = dbTaskDefinition.RevisionTag
	}

	err = definitionmap.WriteAll(hashRevisionMap, taskDefinitionCacheTtl)
	if err != nil {
		log.WithError(err).Error("Failed to add hashRevision map to redis")
		return err
	}

	return nil
}

func ManageTaskDefinitions() {
	refreshInterval := time.Hour * 12
	err := refreshTaskDefinitions(refreshInterval + time.Hour)
	if err != nil {
		utils.ExitWithError(err, "Failed to refresh task definitions", log.NewEntry(log.StandardLogger()))
	}

	definitionmap.SetRefreshDone()
	log.Info("Service started")

	for {
		time.Sleep(refreshInterval)
		log.Info("Starting task definitions update")

		err := refreshTaskDefinitions(refreshInterval + time.Hour)
		if err != nil {
			utils.ExitWithError(err, "Failed to update task definitions", log.NewEntry(log.StandardLogger()))
		}

		log.Info("Task definitions update finished")
	}
}

func refreshIMDSV2Token() {
	for {
		err := utils.RefreshIMDSV2Token()
		if err != nil {
			utils.ExitWithError(err, "Failed to generate IMDSV2 token", log.NewEntry(log.StandardLogger()))
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
		utils.ExitWithError(err, "Failed to init aws session", log.NewEntry(log.StandardLogger()))
	}
	service.AwsSess = awsSess

	err = config.InitDBConnection(config.Conf.DbConnectionString)
	if err != nil {
		utils.ExitWithError(err, "Failed to init DB client", log.NewEntry(log.StandardLogger()))
	}
	defer config.DbConnection.Close()

	err = config.InitCache()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}
	defer config.RedisDefinitionClient.Close()
	defer config.RedisResourcesClient.Close()
	mapper.InitMapperWorkers()

	err = service.InitScalingData()
	if err != nil {
		utils.ExitWithError(err, "Failed to init scaling data", log.NewEntry(log.StandardLogger()))
	}
	service.StartScalers()

	go refreshIMDSV2Token()

	go ManageTaskDefinitions()

	iterationDoneCh := make(chan interface{})
	go ManageTasksAndSession(iterationDoneCh)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	// on shutdown actions
	log.Info("Shutdown scaler ...")
	err = definitionmap.Remove(definitionmap.TaskDefenititonRefreshDone)
	if err != nil {
		log.WithError(err).Error("Failed to unmark task definition refresh")
	}

	// wait for the end of a resources shaping
	log.Info("Waiting for sessions and tasks processors iteration to finish...")
	<-iterationDoneCh
	log.Info("Sessions and tasks processors iteration finished")

	log.Info("Scaler exited")
}
