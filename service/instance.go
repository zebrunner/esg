package service

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
)

var instanceWorker *instanceWatchWorker
var instMutex = &sync.RWMutex{}

func InitInstanceWorker() {
	instanceWorker = &instanceWatchWorker{
		requests: make(map[string]*instanceWaitRequest, 0),
	}
	go instanceWorker.start()
}

type instanceWaitRequest struct {
	ctx                  context.Context
	containerInstanceArn *string
	EssentialErrCh       chan error
	NonEssentialErrCh    chan error
	ResponseCh           chan *ec2.Instance
}

type instanceWatchWorker struct {
	// taskArn -> instanceWaitRequest
	requests map[string]*instanceWaitRequest
}

func (w *instanceWatchWorker) start() {
	svc := ecs.New(AwsSess)
	ec2Svc := ec2.New(AwsSess)
	autoScalingSvc := autoscaling.New(AwsSess)

	for {
		time.Sleep(5 * time.Second)

		for k, v := range w.requests {
			select {
			case <-v.ctx.Done():
				log.WithField("taskArn", k).Trace("instanceWatchWorker: deleting request from map due to the conext deadline")
				delete(w.requests, k)
			default:
				continue
			}
		}

		if len(w.requests) == 0 {
			log.Trace("instanceWatchWorker: no requests found")
			continue
		}

		// ciArnPtrs - array of ptrs for container-instance describing
		ciArnPtrs := make([]*string, 0)
		// ciArnTaskArnsMap - map for organazing response send from instanceWatchWorker
		ciArnTaskArnsMap := make(map[string][]string, 0)
		for taskArn, req := range w.requests {
			if ciArnTaskArnsMap[*req.containerInstanceArn] == nil {
				ciArnTaskArnsMap[*req.containerInstanceArn] = make([]string, 0)
				ciArnPtrs = append(ciArnPtrs, req.containerInstanceArn)
			}
			ciArnTaskArnsMap[*req.containerInstanceArn] = append(ciArnTaskArnsMap[*req.containerInstanceArn], taskArn)
		}

		containerInstances, err := DescribeContainerInstances(ciArnPtrs, svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker: failed to describe container instances.")
			continue
		}

		// ec2IdPtrs - array of ptrs for instance describing
		ec2IdPtrs := make([]*string, 0)
		// ec2IdCiArnsMap - map for organazing response send from instanceWatchWorker
		ec2IdCiArnsMap := make(map[string][]string, 0)
		for _, ci := range containerInstances {
			if *ci.Ec2InstanceId == "" {
				log.WithField("ciArn", *ci.ContainerInstanceArn).Warn("instanceWatchWorker: container instance doesn't contain ec2 instance id")
				continue
			}

			if ec2IdCiArnsMap[*ci.Ec2InstanceId] == nil {
				ec2IdCiArnsMap[*ci.Ec2InstanceId] = make([]string, 0)
				ec2IdPtrs = append(ec2IdPtrs, ci.Ec2InstanceId)
			}
			ec2IdCiArnsMap[*ci.Ec2InstanceId] = append(ec2IdCiArnsMap[*ci.Ec2InstanceId], *ci.ContainerInstanceArn)
		}

		if len(ec2IdPtrs) == 0 {
			log.Trace("instanceWatchWorker: ec2 instance ids list is empty")
			continue
		}

		healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, err := DescribeInstancesStatus(ec2IdPtrs, ec2Svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker: failed to describe instances status.")
			if len(healthyInstanceIdPtrs) == 0 && len(unhealthyInstanceIdPtrs) == 0 {
				log.Trace("instanceWatchWorker: healthy and unhealthy instances lists are empty")
				continue
			}
		}

		if len(unhealthyInstanceIdPtrs) != 0 {
			// stop unhealthy instances
			err := TerminateInstancesInASG(unhealthyInstanceIdPtrs, false, autoScalingSvc)
			if err != nil {
				log.WithError(err).Error("instanceWatchWorker: failed to terminate unhealthy instances.")
				continue
			}

			// send err to errorChan, so new task on new instance could be recreated
			for _, ec2InstanceId := range unhealthyInstanceIdPtrs {
				containerInstanceArns := ec2IdCiArnsMap[*ec2InstanceId]
				for _, containerInstanceArn := range containerInstanceArns {
					taskArns := ciArnTaskArnsMap[containerInstanceArn]
					for _, taskArn := range taskArns {
						err := fmt.Errorf("instance unhealty, status: impaired")
						log.WithField("taskArn", taskArn).WithError(err).Trace("instanceWatchWorker: error sent back to request")

						req := w.requests[taskArn]
						req.NonEssentialErrCh <- err
						delete(w.requests, taskArn)
					}
				}
			}
		}

		if len(healthyInstanceIdPtrs) == 0 {
			log.Warn("instanceWatchWorker: healthy instances are not found")
			continue
		}

		// describing only healthy instances...
		ec2Instances, err := DescribeInstances(healthyInstanceIdPtrs, ec2Svc)
		if err != nil {
			log.Error("instanceWatchWorker: failed to describe ec2 instances")
			continue
		}

		for _, ec2Instance := range ec2Instances {
			ciArns := ec2IdCiArnsMap[*ec2Instance.InstanceId]
			for _, ciArn := range ciArns {
				taskArns := ciArnTaskArnsMap[ciArn]
				for _, taskArn := range taskArns {
					log.WithField("taskArn", taskArn).WithError(err).Trace("instanceWatchWorker: described instance is sent back to request")
					req := w.requests[taskArn]
					req.ResponseCh <- ec2Instance
					delete(w.requests, taskArn)
				}
			}
		}
	}
}

func (w *instanceWatchWorker) waitForInstance(ctx context.Context, task *ecs.Task) *instanceWaitRequest {
	req := instanceWaitRequest{
		ctx:                  ctx,
		EssentialErrCh:       make(chan error, 1),
		NonEssentialErrCh:    make(chan error, 1),
		ResponseCh:           make(chan *ec2.Instance, 1),
		containerInstanceArn: task.ContainerInstanceArn,
	}

	instMutex.Lock()
	w.requests[*task.TaskArn] = &req
	instMutex.Unlock()

	return &req
}
