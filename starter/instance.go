package starter

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/service"
	"github.com/zebrunner/esg/utils"
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
	ResponseCh           chan *ec2Types.Instance
}

type instanceWatchWorker struct {
	// taskArn -> instanceWaitRequest
	requests map[string]*instanceWaitRequest
}

func (w *instanceWatchWorker) start() {
	svc := ecs.NewFromConfig(service.AwsCfg)
	ec2Svc := ec2.NewFromConfig(service.AwsCfg)
	autoScalingSvc := autoscaling.NewFromConfig(service.AwsCfg)
	ctx := context.Background()

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

		// ciArns - array for container-instance describing
		ciArns := make([]string, 0)
		// ciArnTaskArnsMap - map for organazing response send from instanceWatchWorker
		ciArnTaskArnsMap := make(map[string][]string, 0)
		for taskArn, req := range w.requests {
			ciArn := aws.ToString(req.containerInstanceArn)
			if ciArnTaskArnsMap[ciArn] == nil {
				ciArnTaskArnsMap[ciArn] = make([]string, 0)
				ciArns = append(ciArns, ciArn)
			}
			ciArnTaskArnsMap[ciArn] = append(ciArnTaskArnsMap[ciArn], taskArn)
		}

		containerInstances, err := service.DescribeContainerInstances(ctx, ciArns, svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker: failed to describe container instances.")
			continue
		}

		// ec2Ids - array for instance describing
		ec2Ids := make([]string, 0)
		// ec2IdCiArnsMap - map for organazing response send from instanceWatchWorker
		ec2IdCiArnsMap := make(map[string][]string, 0)
		for _, ci := range containerInstances {
			ec2Id := aws.ToString(ci.Ec2InstanceId)
			if ec2Id == "" {
				log.WithField("ciArn", aws.ToString(ci.ContainerInstanceArn)).Warn("instanceWatchWorker: container instance doesn't contain ec2 instance id")
				continue
			}

			if ec2IdCiArnsMap[ec2Id] == nil {
				ec2IdCiArnsMap[ec2Id] = make([]string, 0)
				ec2Ids = append(ec2Ids, ec2Id)
			}
			ec2IdCiArnsMap[ec2Id] = append(ec2IdCiArnsMap[ec2Id], aws.ToString(ci.ContainerInstanceArn))
		}

		if len(ec2Ids) == 0 {
			log.Trace("instanceWatchWorker: ec2 instance ids list is empty")
			continue
		}

		healthyInstanceIds, unhealthyInstanceIds, err := service.DescribeInstancesStatus(ctx, ec2Ids, ec2Svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker: failed to describe instances status.")
			if len(healthyInstanceIds) == 0 && len(unhealthyInstanceIds) == 0 {
				log.Trace("instanceWatchWorker: healthy and unhealthy instances lists are empty")
				continue
			}
		}

		if len(unhealthyInstanceIds) != 0 {
			// stop unhealthy instances
			err := service.TerminateInstancesInASG(ctx, unhealthyInstanceIds, false, autoScalingSvc)
			if err != nil {
				log.WithError(err).Error("instanceWatchWorker: failed to terminate unhealthy instances.")
				continue
			}

			// send err to errorChan, so new task on new instance could be recreated
			for _, ec2InstanceId := range unhealthyInstanceIds {
				containerInstanceArns := ec2IdCiArnsMap[ec2InstanceId]
				for _, containerInstanceArn := range containerInstanceArns {
					taskArns := ciArnTaskArnsMap[containerInstanceArn]
					for _, taskArn := range taskArns {
						err := fmt.Errorf("instance unhealty, status: impaired")
						log.WithField("taskArn", taskArn).WithError(err).Trace("instanceWatchWorker: error sent back to request")

						req := w.requests[taskArn]
						utils.SendToChanIfNotBlocked(req.NonEssentialErrCh, err)
						delete(w.requests, taskArn)
					}
				}
			}
		}

		if len(healthyInstanceIds) == 0 {
			log.Warn("instanceWatchWorker: healthy instances are not found")
			continue
		}

		// describing only healthy instances...
		ec2Instances, err := service.DescribeInstances(ctx, healthyInstanceIds, ec2Svc)
		if err != nil {
			log.Error("instanceWatchWorker: failed to describe ec2 instances")
			continue
		}

		for i := range ec2Instances {
			ec2Instance := &ec2Instances[i]
			ciArns := ec2IdCiArnsMap[aws.ToString(ec2Instance.InstanceId)]
			for _, ciArn := range ciArns {
				taskArns := ciArnTaskArnsMap[ciArn]
				for _, taskArn := range taskArns {
					log.WithField("taskArn", taskArn).WithError(err).Trace("instanceWatchWorker: described instance is sent back to request")
					req := w.requests[taskArn]
					utils.SendToChanIfNotBlocked(req.ResponseCh, ec2Instance)
					delete(w.requests, taskArn)
				}
			}
		}
	}
}

func (w *instanceWatchWorker) waitForInstance(ctx context.Context, task *ecsTypes.Task) *instanceWaitRequest {
	req := instanceWaitRequest{
		ctx:                  ctx,
		EssentialErrCh:       make(chan error),
		NonEssentialErrCh:    make(chan error),
		ResponseCh:           make(chan *ec2Types.Instance),
		containerInstanceArn: task.ContainerInstanceArn,
	}

	instMutex.Lock()
	w.requests[aws.ToString(task.TaskArn)] = &req
	instMutex.Unlock()

	return &req
}
