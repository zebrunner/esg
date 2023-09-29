package taskmap

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

type TaskStatus int

const (
	TaskQueued TaskStatus = iota
	TaskActive
	TaskGeneric // TODO: delete TaskGeneric status when CloseGenericTask() for generic tasks will be called
	TaskPendingToStop
	TaskStopped // Ready for resource usage tracking
)

type StoppedReason string

const (
	TaskStartupFailure     StoppedReason = "task startup failure"
	SessiongStartupFailure StoppedReason = "healthy task failed to start session"
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
	CurrentSessionID string                           `json:",omitempty"`
	StopReason       StoppedReason                    `json:",omitempty"`
	HealthAt         time.Time                        `json:",omitempty"`
	Network          environment.NetworkConfiguration `json:",omitempty"`
	AccessedAt       time.Time                        `json:",omitempty"`
}

func CreateEntity(taskId string, env *environment.ExecutionEnvironment) (*Task, error) {
	err := mapper.UpdateTaskId(env.RouterUUID, taskId)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
		return nil, err
	}
	cachedTask := &Task{
		TaskId:       taskId,
		Capabilities: env.Capabilities,
		Status:       TaskQueued,
		RouterUUID:   env.RouterUUID,
		Workspace:    env.Workspace,
	}

	err = Write(taskId, cachedTask, 0)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
		return nil, err
	}

	return cachedTask, nil
}

func Find(taskId string, rewriteAccessTime bool) (*Task, error) {
	sessionData, err := config.RedisTasksConnection.Get(context.Background(), taskId).Result()
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

	err = config.RedisTasksConnection.Set(context.Background(), taskId, data, expiration).Err()
	if err != nil {
		return err
	}

	if expiration > 0 {
		mapper.SetExpire(task.RouterUUID, expiration)
	}

	return nil
}

func Keys() ([]string, error) {
	return config.RedisTasksConnection.Keys(context.Background(), "*").Result()
}
