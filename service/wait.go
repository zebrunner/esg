package service

import (
	"context"
	"errors"
	"time"
	"sync"
        "strings"

	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

        sessionmap "github.com/zebrunner/esg/sessinonmap"
        "github.com/zebrunner/esg/zebrunner"
)

var taskWaiter *waitWorker
var mutex = &sync.RWMutex{}

func init() {
	taskWaiter = &waitWorker{
		requests: make(map[string]*waitRequest, 1000),
	}
	go taskWaiter.start()
}

type waitRequest struct {
	ctx          context.Context
	responseChan chan *ecs.Task
	errorChan    chan error
	taskId       string
	healthcheck  bool
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
			//TODO: hide to trace
			log.WithField("taskId", k).Info("existing task request")
			select {
			case <-v.ctx.Done():
				log.Error("TODO: implement Zombie task removal")
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

		log.Trace("tasks: ", tasks)
		// Send responses for running tasks
		for _, task := range tasks {
			// use taskId to find and analyze requests
		        taskId := strings.Split(*task.TaskArn, "/")[2]

                        req, ok := w.requests[taskId]
                        if !ok {
                                continue
                        }

                        log.WithField("TaskARN", *task.TaskArn).Info("status: ", *task.LastStatus)

                        if *task.LastStatus == "STOPPED" {
				log.WithField("TaskARN", *task.TaskArn).Info("Task STOPPED")
				if (!req.healthcheck) {
					// IMPORTANT! make sure to call actions before init of req.responseChain!
					// task execution is finished, let's record resource usages
                                        log.WithField("TaskARN", *task.TaskArn).Info("StartedAt: ", *task.StartedAt)
                                        log.WithField("TaskARN", *task.TaskArn).Info("StoppedAt: ", *task.StoppedAt)
                                        startedAt := *task.StartedAt
                                        stoppedAt := *task.StoppedAt
                                        trackTaskResources(taskId, stoppedAt.Sub(startedAt))

					req.responseChan <- task
				} else {
					// stopped state achieved before required healthy state!
                                	req.errorChan <- errors.New("failed to start task: " + *task.StoppedReason)
				}
                                close(req.responseChan)
                                close(req.errorChan)
                                delete(w.requests, taskId)
                        }


                        if *task.LastStatus != "RUNNING" {
				// no sense to verify HEALTHY if task is not started yet or already stopped.
                                continue
                        }

			if (!req.healthcheck) {
				// do not continue with analysis as current task does not require it
				continue
			}

			switch *task.HealthStatus {
			case "UNHEALTHY":
				req.errorChan <- errors.New("failed to start task. HealthStatus - UNHEALTHY")
				close(req.responseChan)
				close(req.errorChan)
				delete(w.requests, taskId)
			case "HEALTHY":
				req.responseChan <- task
				close(req.responseChan)
				close(req.errorChan)
				delete(w.requests, taskId)
			}
		}
	}
}

func (w *waitWorker) waitFor(ctx context.Context, taskId string, healthcheck bool) *waitRequest {
	req := waitRequest{
		ctx:          ctx,
		responseChan: make(chan *ecs.Task),
		errorChan:    make(chan error),
		taskId:       taskId,
		healthcheck: healthcheck,
	}

	// https://medium.com/@luanrubensf/concurrent-map-access-in-go-a6a733c5ffd1
	mutex.Lock()
	w.requests[taskId] = &req
	mutex.Unlock()

	return &req
}

func (w *waitWorker) stopWait(taskId string) {
	req := w.requests[taskId]
	close(req.responseChan)
	delete(w.requests, taskId)
}

func trackTaskResources(taskId string, duration time.Duration) {
	log.WithField("taskId", taskId).Trace("service/wait.go->trackResourceUsage")

	//TODO: do we really need read and remove? Maybe we can port functionality from zebruner/zebrunner.go into this place and don't do redis call twice.
        session, err := getSession(taskId)
        if err != nil {
		log.Error(err)
                return
        }

        err = sessionmap.Remove(session.ID)
        if err != nil {
                log.WithError(err).WithField("id", session.ID).Error("failed to remove task from sessions map")
        }

        zebrunner.TrackResourcesUsage(session, duration)
}


func getSession(id string) (*sessionmap.Session, error) {
        session, err := sessionmap.Find(id, false)
        if err != nil {
                return nil, err
        }

        return session, nil
}
