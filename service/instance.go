package service

import (
	"os"
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
		containerInstanceMap: make(map[string]string, 0),
		ec2Instances:         make(map[string]*ec2.Instance, 0),
	}
	go instanceWorker.start()
}

type instanceWatchWorker struct {
	// containerInstanceArn -> instanceId
	containerInstanceMap map[string]string
	// instanceId -> ec2 instance
	ec2Instances map[string]*ec2.Instance
}

func (w *instanceWatchWorker) start() {
	svc := ecs.New(AwsSess)
	ec2Svc := ec2.New(AwsSess)

	for {
		time.Sleep(5 * time.Second)

		containerInstances, err := DescribeContainerInstances(svc)
		if err != nil {
			log.WithError(err).Error("instanceWatchWorker couldn't describe container instances. Stopping router...")
			os.Exit(1)
		}

		if len(containerInstances) == 0 {
			continue
		}

		instMutex.Lock()
		for _, ci := range containerInstances {
			if *ci.Ec2InstanceId != "" {
				w.containerInstanceMap[*ci.ContainerInstanceArn] = *ci.Ec2InstanceId
			}
		}
		instMutex.Unlock()

		keys := make(map[string]bool)
		ec2InstanceIdPtrs := make([]*string, 0)
		for _, ci := range containerInstances {
			if *ci.Ec2InstanceId == "" {
				continue
			}

			if _, isPresent := keys[*ci.Ec2InstanceId]; !isPresent {
				keys[*ci.Ec2InstanceId] = true
				ec2InstanceIdPtrs = append(ec2InstanceIdPtrs, ci.Ec2InstanceId)
			}
		}

		if len(ec2InstanceIdPtrs) == 0 {
			continue
		}

		// healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, err := DescribeInstancesStatus(ec2InstanceIdPtrs, ec2Svc)
		// if err != nil {
		// 	log.WithError(err).Error("instanceWatchWorker couldn't describe instances status.")
		// }
		 
		// if len(unhealthyInstanceIdPtrs) != 0{
		// 	recover/stop instances
		// }
		
		// if len(healthyInstanceIdPtrs) == 0 {
		// 	continue
		// }

		ec2Instances, err := DescribeInstances(ec2InstanceIdPtrs, ec2Svc)
		if err != nil {
			break
		}

		// save to map
		instMutex.Lock()
		for _, instance := range ec2Instances {
			w.ec2Instances[*instance.InstanceId] = instance
		}
		instMutex.Unlock()
	}
}

func (w *instanceWatchWorker) getInstanceByContainerInstance(containerInstanceArn string) (*ec2.Instance, bool) {
	instMutex.RLock()
	ec2Id, ok := w.containerInstanceMap[containerInstanceArn]
	instMutex.RUnlock()
	if !ok {
		return nil, false
	}

	instMutex.RLock()
	instance, ok := w.ec2Instances[ec2Id]
	instMutex.RUnlock()
	return instance, ok
}
