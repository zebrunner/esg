package taskmap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
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
	HealthAt         *time.Time
	CurrentSessionID string                           `json:",omitempty"`
	StopReason       StoppedReason                    `json:",omitempty"`
	Network          environment.NetworkConfiguration `json:",omitempty"`
	AccessedAt       time.Time                        `json:",omitempty"`
}

func CreateEntity(taskId string, env *environment.ExecutionEnvironment) (*Task, error) {
	err := mapper.UpdateTaskId(env.RouterUUID, taskId)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
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

	err = Write(taskId, cachedTask, 0)
	if err != nil {
		log.WithError(err).Error("Task not cached!")
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

func WriteAll(tasks []Task, expiration time.Duration) error {
	if expiration < 0 {
		return writeAllWithoutUuidUpdate(tasks)
	} else {
		return writeAllWithUuidUpdate(tasks, expiration)
	}
}

func writeAllWithUuidUpdate(tasks []Task, expiration time.Duration) error {
	rdbPipe := config.RedisTasksClient.Pipeline()
	uuidList := make([]string, 0, len(tasks))
	for _, task := range tasks {
		uuidList = append(uuidList, task.RouterUUID)

		data, err := json.Marshal(task)
		if err != nil {
			return err
		}
		rdbPipe.Set(context.Background(), task.TaskId, data, expiration)
	}

	_, err := rdbPipe.Exec(context.Background())
	if err != nil {
		return err
	}

	err = mapper.SetExpireForSeveralRecords(uuidList, expiration)
	return err
}

func writeAllWithoutUuidUpdate(tasks []Task) error {
	rdbPipe := config.RedisTasksClient.Pipeline()
	for _, task := range tasks {
		data, err := json.Marshal(task)
		if err != nil {
			return err
		}
		rdbPipe.Set(context.Background(), task.TaskId, data, -1)
	}

	_, err := rdbPipe.Exec(context.Background())
	return err
}

func Keys() ([]string, error) {
	return config.RedisTasksClient.Keys(context.Background(), "*").Result()
}

func Tasks(taskIdArr []string) ([]Task, error) {
	rdbPipe := config.RedisTasksClient.Pipeline()

	for _, taskId := range taskIdArr {
		rdbPipe.Get(context.Background(), taskId)
	}

	cmds, err := rdbPipe.Exec(context.Background())
	if err != nil {
		return nil, err
	}

	tasks := make([]Task, 0)
	for _, cmd := range cmds {
		data, err := cmd.(*redis.StringCmd).Result()
		if err != nil {
			log.WithError(err).Warn("Failed to get cached task")
			continue
		}

		var task Task
		err = json.Unmarshal([]byte(data), &task)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}

	return tasks, nil
}
