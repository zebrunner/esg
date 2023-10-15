package taskmap

import (
	"context"
	"encoding/json"

	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/config"
)

var (
	updateMutex  = sync.Mutex{}
	writeMutex   = sync.Mutex{}
	updateWorker worker
	writeWorker  worker
)

type WriteItem struct {
	CachedTask Task
	Expiration time.Duration
}

type worker struct {
	rdsConn       *redis.Conn
	tasksToUpdate map[string]WriteItem
	// signals that work is done. Recieved item should not be used
	responseCh chan interface{}
	errCh      chan error
}

func InitTaskmapWorkers() {
	updateWorker = worker{
		rdsConn:       config.RedisTasksClient.Conn(),
		tasksToUpdate: make(map[string]WriteItem, 0),
		responseCh:    make(chan interface{}),
		errCh:         make(chan error),
	}
	go startUpdateWorker()

	writeWorker = worker{
		rdsConn:       config.RedisTasksClient.Conn(),
		tasksToUpdate: make(map[string]WriteItem, 0),
		responseCh:    make(chan interface{}),
		errCh:         make(chan error),
	}
	go startWriteWorker()
}

func (w *worker) flush() {
	w.tasksToUpdate = make(map[string]WriteItem, 0)
	w.responseCh = make(chan interface{})
	w.errCh = make(chan error)
}

func startUpdateWorker() {
	for {
		time.Sleep(1 * time.Second)
		updateMutex.Lock()
		tasksToUpdate := updateWorker.tasksToUpdate
		responseCh := updateWorker.responseCh
		errCh := updateWorker.errCh
		updateWorker.flush()
		updateMutex.Unlock()

		if len(tasksToUpdate) == 0 {
			continue
		}

		rdbFindPipe := updateWorker.rdsConn.Pipeline()
		taskIds := make([]string, 0, len(tasksToUpdate))
		for taskId := range tasksToUpdate {
			taskIds = append(taskIds, taskId)
		}

		tasks, err := cachemaps.FindAll[Task](rdbFindPipe, taskIds)
		if err != nil {
			cachemaps.SendMessageToAllChanns(errCh, err)
			continue
		}

		rdbWritePipeline := updateWorker.rdsConn.Pipeline()
		for _, task := range tasks {
			item := tasksToUpdate[task.TaskId]
			data, _ := json.Marshal(item.CachedTask)
			rdbWritePipeline.Set(context.Background(), task.TaskId, data, item.Expiration)
			if item.Expiration > 0 {
				mapper.ExpireMapper(task.RouterUUID, mapper.WriteItme{Expiration: item.Expiration})
			}
		}

		_, err = rdbWritePipeline.Exec(context.Background())
		if err != nil {
			cachemaps.SendMessageToAllChanns(errCh, err)
		}
		cachemaps.SendMessageToAllChanns(responseCh, 0)
	}
}

func startWriteWorker() {
	for {
		time.Sleep(1 * time.Second)
		writeMutex.Lock()
		tasksToWrite := writeWorker.tasksToUpdate
		responseCh := writeWorker.responseCh
		errCh := writeWorker.errCh
		writeWorker.flush()
		writeMutex.Unlock()

		if len(tasksToWrite) == 0 {
			continue
		}

		rdbWritePipeline := writeWorker.rdsConn.Pipeline()
		for _, item := range tasksToWrite {
			data, _ := json.Marshal(item.CachedTask)
			rdbWritePipeline.Set(context.Background(), item.CachedTask.TaskId, data, item.Expiration)
		}

		_, err := rdbWritePipeline.Exec(context.Background())
		if err != nil {
			cachemaps.SendMessageToAllChanns(errCh, err)
		} else {
			cachemaps.SendMessageToAllChanns(responseCh, 0)
		}
	}
}

func UpdateTask(taskId string, item WriteItem) (<-chan interface{}, <-chan error) {
	updateMutex.Lock()
	updateWorker.tasksToUpdate[taskId] = item
	responseCh := updateWorker.responseCh
	errCh := updateWorker.errCh
	updateMutex.Unlock()
	return responseCh, errCh
}

func WriteTask(taskId string, item WriteItem) (<-chan interface{}, <-chan error) {
	writeMutex.Lock()
	writeWorker.tasksToUpdate[taskId] = item
	responseCh := writeWorker.responseCh
	errCh := writeWorker.errCh
	writeMutex.Unlock()
	return responseCh, errCh
}
