package service

import (
	"context"
	"fmt"
	"strings"
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
		requests: make(map[string]*waitRequest, 2500),
	}
	go taskWaiter.start()
}

type waitRequest struct {
	ctx               context.Context
	taskId            string
	EssentialErrCh    chan error
	NonEssentialErrCh chan error
	ResponseCh        chan *ecs.Task
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
			taskId := strings.Split(*task.TaskArn, "/")[2]
			l := log.WithField(config.TaskIdKey, taskId)
			req, ok := w.requests[taskId]
			if !ok {
				l.Error("RunningTaskWaiter: described task was not found in requests map")
				continue
			}

			if *task.LastStatus == "STOPPED" {
				// #860: Api tests are reexecuted several times
				if strings.Contains(*task.TaskDefinitionArn, "generic") {
					if ok, container := utils.IsTaskFinishedSuccessfully(task); ok {
						l.Info("task already finished")
						select {
						case req.ResponseCh <- task:
						default:
						}
					} else {
						l.WithField("containerName", *container.Name).Error("Generic task stopped: ", *task)
						err := fmt.Errorf("Launcher's container '%s' stopped with reason: %s", *container.Name, *task.StoppedReason)
						if container.ExitCode != nil {
							err = fmt.Errorf("%v, with %v exit code", err, *container.ExitCode)
						}

						select {
						case req.EssentialErrCh <- err:
						default:
						}
					}
				} else {
					l.Error("Task stopped: ", *task)
					select {
					case req.NonEssentialErrCh <- fmt.Errorf("task stopped with reason: %s", *task.StoppedReason):
					default:
					}
				}
				delete(w.requests, taskId)
				continue
			}

			if *task.LastStatus != "RUNNING" {
				// no sense to verify HEALTHY if task is not started yet or already stopped.
				continue
			}

			switch *task.HealthStatus {
			case "UNHEALTHY":
				l.Error("Task unhealthy: ", *task)

				var essential error
				for _, container := range task.Containers {
					if *container.Name == "mitm" && container.ExitCode != nil && *container.ExitCode != 0 {
						essential = fmt.Errorf("failed to start proxy. exit code: %v", *container.ExitCode)
						if container.Reason != nil {
							essential = fmt.Errorf("%s. Reason: %s", essential, *container.Reason)
						}
						break
					}
				}

				if essential != nil {
					select {
					case req.EssentialErrCh <- essential:
					default:
					}
				} else {
					select {
					case req.NonEssentialErrCh <- fmt.Errorf("task unhealthy"):
					default:
					}
				}

				delete(w.requests, taskId)
			case "HEALTHY":
				select {
				case req.ResponseCh <- task:
				default:
				}

				delete(w.requests, taskId)
			}
		}
	}
}

func (w *waitWorker) waitFor(ctx context.Context, taskId string) *waitRequest {
	req := waitRequest{
		ctx:               ctx,
		taskId:            taskId,
		EssentialErrCh:    make(chan error),
		NonEssentialErrCh: make(chan error),
		ResponseCh:        make(chan *ecs.Task),
	}

	// https://medium.com/@luanrubensf/concurrent-map-access-in-go-a6a733c5ffd1
	mutex.Lock()
	w.requests[taskId] = &req
	mutex.Unlock()

	return &req
}
