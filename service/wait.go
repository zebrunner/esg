package service

import (
	"context"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/config"
	"strings"
	"time"
)

var worker *waitWorker

func init() {
	worker = &waitWorker{
		requests: make(map[string]*waitRequest, 1000),
	}
	go worker.start()
}

type waitRequest struct {
	ctx          context.Context
	cancel       func()
	responseChan chan *ecs.Task
	taskId       string
}

func (r *waitRequest) finish() {
	close(r.responseChan)
	r.cancel()
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
		var pages [][]*string
		var page []*string
		for k := range w.requests {
			taskId := k
			page = append(page, &taskId)
			if len(page) == 100 {
				pages = append(pages, page)
				page = []*string{}
			}
		}

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
			if *task.LastStatus == "RUNNING" && *task.HealthStatus == "HEALTHY" {
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
	timeoutCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	req := waitRequest{
		ctx:          timeoutCtx,
		cancel:       cancel,
		responseChan: make(chan *ecs.Task),
		taskId:       taskId,
	}

	w.requests[taskId] = &req

	return &req
}
