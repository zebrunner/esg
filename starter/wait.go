package starter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/service"
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
	ResponseCh        chan *ecsTypes.Task
}

type waitWorker struct {
	requests map[string]*waitRequest
}

func (w *waitWorker) start() {
	svc := ecs.NewFromConfig(service.AwsCfg)

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	for {
		time.Sleep(5 * time.Second)

		if cancel != nil {
			cancel()
		}
		ctx, cancel = context.WithTimeout(context.Background(), service.AwsCallTimeout)

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
		var taskIds []string
		for k := range w.requests {
			taskIds = append(taskIds, k)
		}

		pages := utils.Paginate(taskIds, 100)

		// Send DescribeTasks requests and process errors
		var tasks []ecsTypes.Task
		for _, page := range pages {
			describeTasksInput := &ecs.DescribeTasksInput{
				Cluster: aws.String(config.Conf.AwsCluster),
				Tasks:   page,
			}
			output, err := svc.DescribeTasks(ctx, describeTasksInput)
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
		for i := range tasks {
			task := &tasks[i]
			taskId := strings.Split(aws.ToString(task.TaskArn), "/")[2]
			l := log.WithField(config.TaskIdKey, taskId)
			req, ok := w.requests[taskId]
			if !ok {
				l.Error("RunningTaskWaiter: described task was not found in requests map")
				continue
			}

			if aws.ToString(task.LastStatus) == "STOPPED" {
				// #860: Api tests are reexecuted several times
				if strings.Contains(aws.ToString(task.TaskDefinitionArn), "generic") {
					if ok, container := utils.IsTaskFinishedSuccessfully(task); ok {
						l.Info("task already finished")
						utils.SendToChanIfNotBlocked(req.ResponseCh, task)
					} else {
						l.Error("Generic task stopped: ", *task)

						if container.Reason != nil && strings.Contains(*container.Reason, "CannotPullContainerError") {
							utils.SendToChanIfNotBlocked(req.EssentialErrCh, fmt.Errorf("%s", utils.GetContainerExitReason(container)))
						} else {
							utils.SendToChanIfNotBlocked(req.NonEssentialErrCh, fmt.Errorf("%s", utils.GetContainerExitReason(container)))
						}
					}
				} else {
					l.Error("Task stopped: ", *task)
					utils.SendToChanIfNotBlocked(req.NonEssentialErrCh, fmt.Errorf("task stopped with reason: %s", aws.ToString(task.StoppedReason)))
				}

				delete(w.requests, taskId)
				continue
			}

			if aws.ToString(task.LastStatus) != "RUNNING" {
				// no sense to verify HEALTHY if task is not started yet or already stopped.
				continue
			}

			switch task.HealthStatus {
			case ecsTypes.HealthStatusUnhealthy:
				l.Error("Task unhealthy: ", *task)

				var essential error
				for _, container := range task.Containers {
					if aws.ToString(container.Name) == "mitm" && container.ExitCode != nil && *container.ExitCode != 0 {
						essential = fmt.Errorf("failed to start proxy. exit code: %v", *container.ExitCode)
						if container.Reason != nil {
							essential = fmt.Errorf("%s. Reason: %s", essential, *container.Reason)
						}
						break
					}
				}

				if essential != nil {
					utils.SendToChanIfNotBlocked(req.EssentialErrCh, essential)
				} else {
					utils.SendToChanIfNotBlocked(req.NonEssentialErrCh, fmt.Errorf("task unhealthy"))
				}

				delete(w.requests, taskId)
			case ecsTypes.HealthStatusHealthy:
				utils.SendToChanIfNotBlocked(req.ResponseCh, task)

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
		ResponseCh:        make(chan *ecsTypes.Task),
	}

	// https://medium.com/@luanrubensf/concurrent-map-access-in-go-a6a733c5ffd1
	mutex.Lock()
	w.requests[taskId] = &req
	mutex.Unlock()

	return &req
}
