package taskmap

import (
	"context"
	"encoding/json"

	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
)

var (
	updateWorker cachemaps.RedisWorker[TaskItem]
	writeWorker  cachemaps.RedisWorker[TaskItem]
)

type TaskItem struct {
	CachedTask Task
	Expiration time.Duration
}

// Inits 2 workers and starts them in new thread (update and write workers).
// Update worker -> tries to find records by keys from tasksToUpdate field. Updates records for only found ones.
// Write worker -> creates new records/rewrites existing.
func InitTaskmapWorkers() {
	updateWorker = cachemaps.CreateRedisWorker[TaskItem](config.RedisTasksClient, updateRecords)
	go updateWorker.Start(time.Second * 1)

	writeWorker = cachemaps.CreateRedisWorker[TaskItem](config.RedisTasksClient, writeRecords)
	go writeWorker.Start(time.Second * 1)
}

func updateRecords(rdsConn *redis.Conn, items map[string]TaskItem) error {
	rdbFindPipe := rdsConn.Pipeline()
	taskIds := make([]string, 0, len(items))
	for taskId := range items {
		taskIds = append(taskIds, taskId)
	}

	tasks, err := cachemaps.FindAll[Task](rdbFindPipe, taskIds)
	if err != nil {
		log.WithField("taskIds", taskIds).WithError(err).Error("Failed to find cached tasks")
		return err
	}

	rdbWritePipeline := rdsConn.Pipeline()
	// always -> len(tasks) <= len(items)
	for _, task := range tasks {
		item := items[task.TaskId]
		data, _ := json.Marshal(&item.CachedTask)
		rdbWritePipeline.Set(context.Background(), task.TaskId, data, item.Expiration)
		if item.Expiration > 0 {
			mapper.ExpireMapper(task.RouterUUID, item.Expiration)
		}
	}

	_, err = rdbWritePipeline.Exec(context.Background())
	return err
}

func writeRecords(rdsConn *redis.Conn, items map[string]TaskItem) error {
	rdbWritePipeline := rdsConn.Pipeline()
	for _, item := range items {
		data, err := json.Marshal(&item.CachedTask)
		if err != nil {
			log.WithError(err).WithField(config.TaskIdKey, item.CachedTask.TaskId).Error("Failed to marshal record")
			continue
		}

		rdbWritePipeline.Set(context.Background(), item.CachedTask.TaskId, data, item.Expiration)
		if item.Expiration > 0 {
			mapper.ExpireMapper(item.CachedTask.RouterUUID, item.Expiration)
		}
	}

	_, err := rdbWritePipeline.Exec(context.Background())
	return err
}

/*
To wait for response implement select switch construction
	select {
	case err := <-errCh:
		...
	case <-responseCh:
	}
*/
func UpdateTask(cachedTask Task, expiration time.Duration) (<-chan interface{}, <-chan error) {
	return updateWorker.AppendToWorker(cachedTask.TaskId, TaskItem{CachedTask: cachedTask, Expiration: expiration})
}

/*
To wait for response implement select switch construction
	select {
	case err := <-errCh:
		...
	case <-responseCh:
	}
*/
func WriteTask(cachedTask Task, expiration time.Duration) (<-chan interface{}, <-chan error) {
	return writeWorker.AppendToWorker(cachedTask.TaskId, TaskItem{CachedTask: cachedTask, Expiration: expiration})
}
