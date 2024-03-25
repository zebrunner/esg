package taskmap

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

type TaskStatus int

const (
	TaskQueued TaskStatus = iota
	TaskActive
	TaskCypress
	TaskPendingToStop
	TaskStopped // Ready for resource usage tracking
)

type StoppedReason string

const (
	TaskStartupFailure     StoppedReason = "task startup failure"
	TaskUnhealthy          StoppedReason = "task aborted due to unhealthy status"
	TaskMaxTimeout         StoppedReason = "task aborted due to the max timeout"
	TaskAborted            StoppedReason = "task aborted"
	TaskFinished           StoppedReason = "task finished"
	TaskLost               StoppedReason = "task aborted as it wasn't found in cache"
)

type Task struct {
	TaskId           string
	Capabilities     *capabilities.Capabilities
	Status           TaskStatus
	UsageTracked     bool
	Workspace        string
	RouterUUID       string
	HealthAt         *time.Time
	CurrentSessionID string                           `json:",omitempty"`
	StopReason       StoppedReason                    `json:",omitempty"`
	Network          environment.NetworkConfiguration `json:",omitempty"`
	AccessedAt       time.Time                        `json:",omitempty"`
}

// Creates new record in tasks redis db and updates mapper uuid record with new taskId. Resets existing expiration on mapper uuid record
func CreateEntity(taskId string, env *environment.ExecutionEnvironment) (*Task, error) {
	err := mapper.WriteMapperRecord(mapper.IdMapper{RouterUUID: env.RouterUUID, TaskId: taskId}, 0)
	if err != nil {
		log.WithField(config.TaskIdKey, taskId).WithError(err).Error("Task not cached!")
		return nil, err
	}

	creationTime := time.Now()
	cachedTask := &Task{
		TaskId:       taskId,
		Capabilities: env.Capabilities,
		Status:       TaskQueued,
		RouterUUID:   env.RouterUUID,
		Workspace:    env.Workspace,
		HealthAt:     &creationTime,
	}

	err = WriteTask(*cachedTask, 0)
	if err != nil {
		log.WithField(config.TaskIdKey, taskId).WithError(err).Error("Task not cached!")
		return nil, err
	}

	return cachedTask, nil
}

func Find(taskId string, rewriteAccessTime bool) (*Task, error) {
	sessionData, err := config.RedisTasksClient.Get(context.Background(), taskId).Result()
	if err != nil {
		return nil, err
	}

	var task Task
	err = json.Unmarshal([]byte(sessionData), &task)
	if err != nil {
		return nil, err
	}

	if rewriteAccessTime {
		task.AccessedAt = time.Now()
		// -1 keeps the same ttl
		err = Write(taskId, &task, -1)

		if err != nil {
			log.WithError(err).Error("Failed to update last access time")
		}
	}

	return &task, nil
}

func FindByRouterUUID(routerUUID string) (*Task, error) {
	taskId, err := mapper.FindTaskId(routerUUID)
	if err != nil {
		return nil, err
	}

	return Find(*taskId, true)
}

func Write(taskId string, task *Task, expiration time.Duration) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}

	err = config.RedisTasksClient.Set(context.Background(), taskId, data, expiration).Err()
	if err != nil {
		return err
	}

	if expiration > 0 {
		mapper.SetExpire(task.RouterUUID, expiration)
	}

	return nil
}

// Writes all passed tasks to redis db by pipeline.
func WriteAll(tasks []Task, expiration time.Duration) error {
	rdbPipe := config.RedisTasksClient.Pipeline()
	tasksMap := make(map[string]Task, len(tasks))
	if expiration > 0 {
		uuidList := make([]string, 0, len(tasksMap))
		for _, task := range tasks {
			uuidList = append(uuidList, task.RouterUUID)
			tasksMap[task.TaskId] = task
		}

		err := cachemaps.WriteAll(rdbPipe, tasksMap, expiration)
		if err != nil {
			return err
		}

		return mapper.SetExpireForSeveralRecords(uuidList, expiration)
	} else {
		for _, task := range tasks {
			tasksMap[task.TaskId] = task
		}
		return cachemaps.WriteAll(rdbPipe, tasksMap, expiration)
	}
}

func Keys() ([]string, error) {
	keysSet := make(map[string]string)
	iter := config.RedisTasksClient.Scan(context.Background(), 0, "*", 50).Iterator()
	for iter.Next(context.Background()) {
		key := iter.Val()
		keysSet[key] = key
	}

	if err := iter.Err(); err != nil {
		log.WithError(err).Error("Failed to get all keys")
		return nil, err
	}

	i := 0
	keys := make([]string, len(keysSet))
	for key := range keysSet {
		keys[i] = key
		i++
	}

	return keys, nil
}

// Returns cached tasks by passed taskIdArr from tasks redis db
func Tasks(taskIdArr []string) ([]Task, error) {
	return cachemaps.FindAll[Task](config.RedisTasksClient.Pipeline(), taskIdArr)
}
