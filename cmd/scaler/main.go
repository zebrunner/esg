package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/utilsmap"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"

	awsSession "github.com/aws/aws-sdk-go/aws/session"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

	"github.com/zebrunner/esg/zebrunner"
)

func stopLostTasks(ctx context.Context, svc *ecs.ECS, wg *sync.WaitGroup) {
	defer wg.Done()

	// still use ServiceStartupTimeout for timer, however the task will be removed if it has been running for LostTaskCooldownTimeout
	timer := utils.CreateTimer(config.Conf.ServiceStartupTimeout/2 + 1*time.Minute)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer():
			routerUuids, err := cachemaps.GetKeys(cachemaps.TASK)
			if err != nil {
				log.WithError(err).Warn("Failed to get list of taskmap keys!")
				continue
			}

			mapperEntities, err := mapper.FindAll(routerUuids)
			if err != nil {
				log.WithError(err).Warn("Failed to get cached mapper enties")
				continue
			}

			taskIds := make([]string, 0)
			for _, mapperEntity := range mapperEntities {
				if mapperEntity.TaskId != "" {
					taskIds = append(taskIds, mapperEntity.TaskId)
				}
			}

			taskArns, err := service.GetClusterTasksArn(svc)
			if err != nil {
				log.WithError(err).Error("Error on ecs list-tasks operation")
				continue
			}

			tasksToDescribe := make([]string, 0)

			for _, taskArn := range taskArns {
				isFound := false
				for _, key := range taskIds {
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
				continue
			}

			tasks, err := service.DescribeTasks(tasksToDescribe)
			if err != nil {
				log.WithError(err).Warn("StopLostTasks(): failed to describe lost tasks")
				continue
			}

			for _, task := range tasks {
				if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
					if time.Since(*task.StartedAt) <= config.Conf.LostTaskCooldownTimeout {
						continue
					}

					if task.Group != nil && *task.Group == "service:linux-exporter" {
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
		}
	}
}

func stopUnhealthyTasks(ctx context.Context, svc *ecs.ECS, wg *sync.WaitGroup) {
	defer wg.Done()

	timer := utils.CreateTimer(10 * time.Minute)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer():
			routerUuids, err := cachemaps.GetKeys(cachemaps.TASK)
			if err != nil {
				log.WithError(err).Warn("Failed to get list of taskmap keys!")
				continue
			}

			mapperEntities, err := mapper.FindAll(routerUuids)
			if err != nil {
				log.WithError(err).Warn("Failed to get cached mapper enties")
				continue
			}

			taskIds := make([]string, 0)
			for _, mapperEntity := range mapperEntities {
				if mapperEntity.TaskId != "" {
					taskIds = append(taskIds, mapperEntity.TaskId)
				}
			}

			if len(taskIds) <= 0 {
				continue
			}

			tasks := service.GetTasksByTaskIds(taskIds, svc)

			if len(tasks) <= 0 {
				continue
			}

			taskIdMapperMap := make(map[string]mapper.Mapper)
			for _, mapperEntity := range mapperEntities {
				if mapperEntity.TaskId != "" {
					taskIdMapperMap[mapperEntity.TaskId] = mapperEntity
				}
			}

			for _, task := range tasks {
				taskId := strings.Split(*task.TaskArn, "/")[2]
				l := log.WithField(config.TaskIdKey, taskId)
				// stop zombie and UNHEALTHY tasks that are not pending for stop.
				// resource usage register and taskId mark for removal is performed only for stopped tasks
				if *task.LastStatus == "RUNNING" && *task.DesiredStatus != "STOPPED" {
					mapperEntity, ok := taskIdMapperMap[taskId]
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
		}
	}
}

func trackResourceUsage(ctx context.Context, svc *ecs.ECS, wg *sync.WaitGroup) {
	defer wg.Done()

	timer := utils.CreateTimer(1 * time.Minute)

	for {
		select {

		case <-ctx.Done():
			return
		case <-timer():

			routerUuids, err := cachemaps.GetKeys(cachemaps.TASK)
			if err != nil {
				log.WithError(err).Warn("Failed to get list of taskmap keys!")
				continue
			}

			mapperEntities, err := mapper.FindAll(routerUuids)
			if err != nil {
				log.WithError(err).Warn("Failed to get cached mapper enties")
				continue
			}

			taskIds := make([]string, 0)
			for _, mapperEntity := range mapperEntities {
				if mapperEntity.TaskId != "" {
					taskIds = append(taskIds, mapperEntity.TaskId)
				}
			}

			if len(taskIds) <= 0 {
				continue
			}

			tasks := service.GetTasksByTaskIds(taskIds, svc)

			if len(tasks) <= 0 {
				continue
			}

			taskIdMapperMap := make(map[string]mapper.Mapper)
			for _, mapperEntity := range mapperEntities {
				if mapperEntity.TaskId != "" {
					taskIdMapperMap[mapperEntity.TaskId] = mapperEntity
				}
			}

			// analyze tasks response
			tasksCacheToUpdate := make(map[string]mapper.Mapper)
			tasksToTrack := make(map[*mapper.Mapper]*ecs.Task)
			for _, task := range tasks {
				taskId := strings.Split(*task.TaskArn, "/")[2]
				l := log.WithField(config.TaskIdKey, taskId)

				// tracking task only when execution is stopped
				if *task.LastStatus != "STOPPED" {
					continue
				}

				// for tracking task should be cached
				cachedTask, ok := taskIdMapperMap[taskId]
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
				tasksCacheToUpdate[cachedTask.RouterUUID] = cachedTask

				l = l.WithField(config.RouterUUID, cachedTask.RouterUUID)
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
			err = mapper.WriteShapedEntities(tasksCacheToUpdate, 5*time.Minute)
			if err != nil {
				log.WithError(err).Error("Failed to update tracked tasks!")
			} else {
				for cachedTask, task := range tasksToTrack {
					zebrunner.TrackResourcesUsage(cachedTask, task)
				}
			}
		}
	}
}

func stopIdleSessions(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	timer := utils.CreateTimer(config.Conf.IdleTimeout)

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer():
			routerUuids, err := cachemaps.GetKeys(cachemaps.SESSION)
			if err != nil {
				log.WithError(err).Error("Failed to list uuid keys from sessions set!")
				continue
			}

			mapperEntities, err := mapper.FindAll(routerUuids)
			if err != nil {
				log.WithError(err).Error("Failed to get mapper entities!")
				continue
			}

			var sessionsWg sync.WaitGroup
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
				mapperEntity, err := mapper.Find(mapperEntity.RouterUUID)
				if err != nil {
					l.WithError(err).Error("Failed to get mapperEntity!")
					continue
				}

				idle = mapperEntity.IsIdle()
				if !idle {
					continue
				}

				sessionsWg.Add(1)
				go func(m *mapper.Mapper, l *log.Entry, wg *sync.WaitGroup) {
					defer wg.Done()

					selenium.CloseSession(m)

					err = service.StopTask(*m, mapper.SessionIdleTimeout)
					if err != nil {
						l.WithError(err).Error("Failed to stop idle driver task!")
					} else {
						l.Warn("task aborted due to the session idle timeout")
					}
				}(mapperEntity, l, &sessionsWg)
			}
			sessionsWg.Wait()

		}
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
	defer func() {
		config.CloseConnections()
	}()

	flag.Parse()

	log.SetLevel(config.Conf.ParseLogLevel())

	awsSess, err := service.InitAws()
	if err != nil {
		utils.ExitWithError(err, "Failed to init aws session", log.NewEntry(log.StandardLogger()))
	}
	service.AwsSess = awsSess

	err = config.InitRedisClusterConnection()
	if err != nil {
		utils.ExitWithError(err, "Failed to init redis connection", log.NewEntry(log.StandardLogger()))
	}

	mapper.InitMapperWorkers()
	err = utilsmap.ScalerVersion.Set(config.Version)
	if err != nil {
		log.WithError(err).Error("Failed to set scaler version in cache")
	}

	scalersMap, err := service.InitScalingData()
	if err != nil {
		utils.ExitWithError(err, "Failed to init scaling data", log.NewEntry(log.StandardLogger()))
	}
	service.StartScalers(scalersMap)

	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		exit := make(chan os.Signal, 1)
		signal.Notify(exit, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
		<-exit
		log.Info("Shutdown scaler ...")
		cancel()
	}()

	// Skip IMDS calls when static AWS credentials are configured
	if !config.Conf.HasStaticCredentials() {
		go refreshIMDSV2Token()
	} else {
		log.Info("Static AWS credentials configured, skipping IMDS token refresh")
	}

	var wg sync.WaitGroup

	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		utils.ExitWithError(err, "Failed to create AWS session", log.NewEntry(log.StandardLogger()))
	} else {
		svc := ecs.New(session)

		wg.Add(1)
		go stopIdleSessions(ctx, &wg)

		wg.Add(1)
		go stopLostTasks(ctx, svc, &wg)

		wg.Add(1)
		go stopUnhealthyTasks(ctx, svc, &wg)

		wg.Add(1)
		go trackResourceUsage(ctx, svc, &wg)

		log.Info("Service started")
	}

	wg.Wait()
	log.Info("Scaler exited")
}
