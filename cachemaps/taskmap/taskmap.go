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
	ID               string
	Capabilities     *capabilities.Capabilities
	Status           TaskStatus
	CurrentSessionID string        `json:",omitempty"`
	StopReason       StoppedReason `json:",omitempty"`
	UsageTracked     bool
	Workspace        string
}

func CreateEntity(id string, env *environment.ExecutionEnvironment) (*Task, error) {
	taskCache := &Task{
		ID:           id,
		Capabilities: env.Capabilities,
		Status:       TaskQueued,
		Workspace:    env.Workspace,
	}

	err := Write(taskCache.ID, taskCache, 0)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
		return nil, err
	}

	return taskCache, nil
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

func Write(id string, task *Task, expiration time.Duration) error {
	data, err := json.Marshal(task)
	if err != nil {
		return err
	}

	err = config.RedisTasksConnection.Set(context.Background(), id, data, expiration).Err()
	if err != nil {
		return err
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
