package service

import (
	"context"
	"fmt"
	"sync"
	"time"

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

	for {
		time.Sleep(5 * time.Second)

		for k, v := range w.requests {
			select {
			case <-v.ctx.Done():
				delete(w.requests, k)
			default:
				continue
			}
		}

		if len(w.requests) == 0 {
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
				continue
			}

			if ec2IdCiArnsMap[*ci.Ec2InstanceId] == nil {
				ec2IdCiArnsMap[*ci.Ec2InstanceId] = make([]string, 0)
				ec2IdPtrs = append(ec2IdPtrs, ci.Ec2InstanceId)
			}
			ec2IdCiArnsMap[*ci.Ec2InstanceId] = append(ec2IdCiArnsMap[*ci.Ec2InstanceId], *ci.ContainerInstanceArn)
		}

		if len(ec2IdPtrs) == 0 {
			continue
		}

		healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, err := DescribeInstancesStatus(ec2IdPtrs, ec2Svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker: failed to describe instances status.")
			if len(healthyInstanceIdPtrs) == 0 && len(unhealthyInstanceIdPtrs) == 0 {
				continue
			}
		}

		if len(unhealthyInstanceIdPtrs) != 0 {
			// stop unhealthy instances
			err := TerminateInstances(unhealthyInstanceIdPtrs, ec2Svc)
			if err != nil {
				log.WithError(err).Error("instanceWatchWorker: failed to terminate instances.")
				break
			}

			// send err to errorChan, so new task on new instance could be recreated
			for _, ec2InstanceId := range unhealthyInstanceIdPtrs {
				containerInstanceArns := ec2IdCiArnsMap[*ec2InstanceId]
				for _, containerInstanceArn := range containerInstanceArns {
					taskArns := ciArnTaskArnsMap[containerInstanceArn]
					for _, taskArn := range taskArns {
						req := w.requests[taskArn]
						req.NonEssentialErrCh <- fmt.Errorf("instance unhealty, status: impaired")
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
			ciArns := ec2IdCiArnsMap[*ec2Instance.InstanceId]
			for _, ciArn := range ciArns {
				taskArns := ciArnTaskArnsMap[ciArn]
				for _, taskArn := range taskArns {
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
		EssentialErrCh:       make(chan error),
		NonEssentialErrCh:    make(chan error),
		ResponseCh:           make(chan *ec2.Instance),
		containerInstanceArn: task.ContainerInstanceArn,
	}

	instMutex.Lock()
	w.requests[*task.TaskArn] = &req
	instMutex.Unlock()

	return &req
}
