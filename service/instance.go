package service

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
)

var instanceWorker *instanceWatchWorker
var instMutex = &sync.RWMutex{}

func init() {
	instanceWorker = &instanceWatchWorker{
		requests: make(map[string]*instanceWaitRequest, 100),
	}
	go instanceWorker.start()
}

type instanceWaitRequest struct {
	ctx                  context.Context
	responseChan         chan *ec2.Instance
	errorChan            chan error
	containerInstanceArn *string
}

type instanceWatchWorker struct {
	// taskArn -> instanceWaitRequest
	requests map[string]*instanceWaitRequest
}

func (w *instanceWatchWorker) start() {
	svc := ecs.New(AwsSess)
	ec2Svc := ec2.New(AwsSess)

	for {
		time.Sleep(5 * time.Second)

		for k, v := range w.requests {
			select {
			case <-v.ctx.Done():
				close(v.errorChan)
				close(v.responseChan)
				delete(w.requests, k)
			default:
				continue
			}
		}

		if len(w.requests) == 0 {
			continue
		}

		// containerInstanceIdPtrs - array of ptrs for container-instance describing
		containerInstanceIdPtrs := make([]*string, 0)
		// containerInstanceArnTaskArnMap - map for organazing response send from instanceWatchWorker
		containerInstanceArnTaskArnMap := make(map[string][]string, 0)
		for taskArn, req := range w.requests {
			if containerInstanceArnTaskArnMap[*req.containerInstanceArn] == nil {
				containerInstanceArnTaskArnMap[*req.containerInstanceArn] = make([]string, 0)
				containerInstanceIdPtrs = append(containerInstanceIdPtrs, req.containerInstanceArn)
			}
			containerInstanceArnTaskArnMap[*req.containerInstanceArn] = append(containerInstanceArnTaskArnMap[*req.containerInstanceArn], taskArn)
		}

		containerInstances, err := DescribeContainerInstances(containerInstanceIdPtrs, svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker: couldn't describe container instances.")
			continue
		}

		// ec2InstanceIdPtrs - array of ptrs for instance describing
		ec2InstanceIdPtrs := make([]*string, 0)
		// instanceIdTaskArnMap - map for organazing response send from instanceWatchWorker
		instanceIdTaskArnMap := make(map[string][]string, 0)
		for _, ci := range containerInstances {
			if *ci.Ec2InstanceId == "" {
				continue
			}

			if instanceIdTaskArnMap[*ci.Ec2InstanceId] == nil {
				instanceIdTaskArnMap[*ci.Ec2InstanceId] = make([]string, 0)
				ec2InstanceIdPtrs = append(ec2InstanceIdPtrs, ci.Ec2InstanceId)
			}
			instanceIdTaskArnMap[*ci.Ec2InstanceId] = append(instanceIdTaskArnMap[*ci.Ec2InstanceId], *ci.ContainerInstanceArn)
		}

		if len(ec2InstanceIdPtrs) == 0 {
			continue
		}

		healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, err := DescribeInstancesStatus(ec2InstanceIdPtrs, ec2Svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker couldn't describe instances status.")
		}

		if len(unhealthyInstanceIdPtrs) != 0 {
			// stop unhealthy instances
			StopInstances(unhealthyInstanceIdPtrs, ec2Svc)

			// send err to errorChan, so new task on new instance could be recreated
			for _, ec2InstanceId := range unhealthyInstanceIdPtrs {
				containerInstanceArns := instanceIdTaskArnMap[*ec2InstanceId]
				for _, containerInstanceArn := range containerInstanceArns {
					taskArns := containerInstanceArnTaskArnMap[containerInstanceArn]
					for _, taskArn := range taskArns {
						req := w.requests[taskArn]
						req.errorChan <- errors.New("failed to get instance. InstanceStatus - impaired")
						close(req.errorChan)
						close(req.responseChan)
						delete(w.requests, taskArn)
					}
				}
			}
		}

		if len(healthyInstanceIdPtrs) == 0 {
			continue
		}

		// describing only healthy instances...
		ec2Instances, err := DescribeInstances(healthyInstanceIdPtrs, ec2Svc)
		if err != nil {
			log.Error("instanceWatchWorker: failed to describe ec2 instances")
			continue
		}

		for _, ec2Instance := range ec2Instances {
			containerInstanceArns := instanceIdTaskArnMap[*ec2Instance.InstanceId]
			for _, containerInstanceArn := range containerInstanceArns {
				taskArns := containerInstanceArnTaskArnMap[containerInstanceArn]
				for _, taskArn := range taskArns {
					req := w.requests[taskArn]
					req.responseChan <- ec2Instance
					close(req.errorChan)
					close(req.responseChan)
					delete(w.requests, taskArn)
				}
			}
		}
	}
}

func (w *instanceWatchWorker) waitForInstance(ctx context.Context, task *ecs.Task) *instanceWaitRequest {
	req := instanceWaitRequest{
		ctx:                  ctx,
		responseChan:         make(chan *ec2.Instance),
		errorChan:            make(chan error),
		containerInstanceArn: task.ContainerInstanceArn,
	}

	instMutex.Lock()
	w.requests[*task.TaskArn] = &req
	instMutex.Unlock()

	return &req
}
