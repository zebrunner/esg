package taskmap

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"

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
	UUID             string
	CurrentSessionID string                           `json:",omitempty"`
	StopReason       StoppedReason                    `json:",omitempty"`
	HealthAt         time.Time                        `json:",omitempty"`
	Network          environment.NetworkConfiguration `json:",omitempty"`
}

func CreateEntity(taskId string, env *environment.ExecutionEnvironment) (*Task, error) {
	err := write(env.UUID, &UuidMapper{UUID: env.UUID, TaskId: taskId}, 0)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
		return nil, err
	}

	cachedTask := &Task{
		TaskId:       taskId,
		Capabilities: env.Capabilities,
		Status:       TaskQueued,
		UUID:         env.UUID,
		Workspace:    env.Workspace,
	}

	err = Write(cachedTask.TaskId, cachedTask, 0)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
		return nil, err
	}

	return cachedTask, nil
}

func Find(id string) (*Task, error) {
	sessionData, err := config.RedisTasksConnection.Get(context.Background(), id).Result()
	if err != nil {
		return nil, err
	}

	var task Task
	err = json.Unmarshal([]byte(sessionData), &task)
	if err != nil {
		return nil, err
	}

	return &task, nil
}

func FindByUuid(uuid string) (*Task, error) {
	taskId, err := findTaskId(uuid)
	if err != nil {
		return nil, err
	}

	return Find(*taskId)
}

func Write(id string, task *Task, expiration time.Duration) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}

	err = config.RedisTasksConnection.Set(context.Background(), id, data, expiration).Err()
	if err != nil {
		return err
	}

	if expiration > 0 {
		write(task.UUID, &UuidMapper{UUID: task.UUID, TaskId: task.TaskId}, expiration)
	}

	return nil
}

func Remove(id string) error {
	err := config.RedisTasksConnection.Del(context.Background(), id).Err()
	if err != nil {
		return err
	}

	return nil
}

func Keys() ([]string, error) {
	keys, err := config.RedisTasksConnection.Keys(context.Background(), "*").Result()

	return keys, err
}
