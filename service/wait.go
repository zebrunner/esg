package service

import (
	"context"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/config"
)

var taskWaiter *waitWorker

func init() {
	taskWaiter = &waitWorker{
		requests: make(map[string]*waitRequest, 1000),
	}
	go taskWaiter.start()
}

type waitRequest struct {
	ctx          context.Context
	responseChan chan *ecs.Task
	taskId       string
}

func (r *waitRequest) finish() {
	close(r.responseChan)
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
			output, err := svc.DescribeTasks(&describeTasksInput)
			if err != nil {
				// TODO: write to logs
				continue
			}

			if len(output.Failures) != 0 {
				// TODO: write to logs
				continue
			}

			if len(output.Tasks) == 0 {
				// TODO: write to logs
				continue
			}

			tasks = append(tasks, output.Tasks...)
		}

		// Send responses for running tasks
		for _, task := range tasks {
			if *task.LastStatus == "RUNNING" {
				taskId := strings.Split(*task.TaskArn, "/")[2]
				req := w.requests[taskId]

				taskCopy := task
				req.responseChan <- taskCopy
				req.finish()
				delete(w.requests, taskId)
			}
		}
	}
}

func (w *waitWorker) waitFor(ctx context.Context, taskId string) *waitRequest {
	req := waitRequest{
		ctx:          ctx,
		responseChan: make(chan *ecs.Task),
		taskId:       taskId,
	}

	w.requests[taskId] = &req

	return &req
}
