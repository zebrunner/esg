package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

var taskWaiter *waitWorker
var mutex = &sync.RWMutex{}

func InitWaitWorker() {
	taskWaiter = &waitWorker{
		requests: make(map[string]*waitRequest, 1000),
	}
	go taskWaiter.start()
}

type waitRequest struct {
	ctx          context.Context
	responseChan chan<- *ecs.Task
	errorChan    chan<- error
	taskId       string
}

type waitWorker struct {
	requests map[string]*waitRequest
}

func (w *waitWorker) start() {
	svc := ecs.New(AwsSess)

	for {
		time.Sleep(5 * time.Second)

		if len(w.requests) == 0 {
			continue
		}

		for k, v := range w.requests {
			select {
			case <-v.ctx.Done():
				delete(w.requests, k)
			default:
				continue
			}
		}

		// Construct pages with 100 or fewer elements for requests. 100 is an AWS limitation for Describe* requests
		var taskIdsPtrs []*string
		for k := range w.requests {
			taskId := k
			taskIdsPtrs = append(taskIdsPtrs, &taskId)
		}

		pages := paginate(taskIdsPtrs, 100)

		// Send DescribeTasks requests and process errors
		var tasks []*ecs.Task
		for _, page := range pages {
			describeTasksInput := ecs.DescribeTasksInput{
				Cluster: &config.Conf.AwsCluster,
				Tasks:   page,
			}
			output, err := utils.RetryThrottling(svc.DescribeTasks)(&describeTasksInput)
			if err != nil {
				log.WithError(err).Error("RunningTaskWaiter: failed to describe tasks")
				continue
			}

			if len(output.Failures) != 0 {
				log.WithField("failures", output.Failures).Error("RunningTaskWaiter: failed to describe tasks")
				continue
			}

			if len(output.Tasks) == 0 {
				log.Error("RunningTaskWaiter: failed to describe tasks. No tasks in response")
				continue
			}

			tasks = append(tasks, output.Tasks...)
		}

		// Send responses for running tasks
		for _, task := range tasks {
			req, ok := w.requests[*task.TaskArn]
			if !ok {
				continue
			}

			if *task.LastStatus == "STOPPED" {
				log.Error("Task stopped: ", *task)
				req.errorChan <- errors.New("task stopped with reason: " + *task.StoppedReason)
				delete(w.requests, *task.TaskArn)
			}

			if *task.LastStatus != "RUNNING" {
				// no sense to verify HEALTHY if task is not started yet or already stopped.
				continue
			}

			switch *task.HealthStatus {
			case "UNHEALTHY":
				log.Error("Task unhealthy: ", *task)
				req.errorChan <- errors.New("task unhealthy")
				delete(w.requests, *task.TaskArn)
			case "HEALTHY":
				req.responseChan <- task
				delete(w.requests, *task.TaskArn)
			}
		}
	}
}

func (w *waitWorker) waitFor(ctx context.Context, taskId string) *waitRequest {
	req := waitRequest{
		ctx:          ctx,
		responseChan: make(chan<- *ecs.Task),
		errorChan:    make(chan<- error),
		taskId:       taskId,
	}

	// https://medium.com/@luanrubensf/concurrent-map-access-in-go-a6a733c5ffd1
	mutex.Lock()
	w.requests[taskId] = &req
	mutex.Unlock()

	return &req
}
