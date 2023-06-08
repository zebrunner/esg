package service

import (
	"math"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"

	log "github.com/sirupsen/logrus"
)

var instanceWorker *instanceWatchWorker
var instMutex = &sync.RWMutex{}

func init() {
	instanceWorker = &instanceWatchWorker{
		containerInstances: map[string]*ecs.ContainerInstance{},
		ec2Instances:       map[string]*ec2.Instance{},
	}
	go instanceWorker.start()
}

type instanceWatchWorker struct {
	containerInstances map[string]*ecs.ContainerInstance
	ec2Instances       map[string]*ec2.Instance
}

func paginate[T interface{}](l []T, size int) [][]T {
	numPages := int(math.Ceil(float64(len(l)) / float64(size)))
	pages := make([][]T, numPages)
	for i := 0; i < numPages; i++ {
		left := i * size
		right := (i + 1) * size
		if right > len(l) {
			right = len(l)
		}
		pages[i] = l[left:right]
	}

	return pages
}

func (w *instanceWatchWorker) start() {
	svc := ecs.New(AwsSess)
	ec2Svc := ec2.New(AwsSess)

	for {
		time.Sleep(5 * time.Second)

		// List all containerInstances
		var containerInstanceIds []*string
		listInput := ecs.ListContainerInstancesInput{
			Cluster: &config.Conf.AwsCluster,
		}
		for {
			listResult, err := utils.RetryThrottling(svc.ListContainerInstances)(&listInput)
			if err != nil {
				log.WithField("list", listInput).WithField("error", err).Error("Failed to ListContainerInstances!")
				return // exit from method as cluster instances can't be detected
			}

			containerInstanceIds = append(containerInstanceIds, listResult.ContainerInstanceArns...)

			if listResult.NextToken != nil {
				listInput.NextToken = listResult.NextToken
			} else {
				break
			}
		}

		// Describe all container instances
		var containerInstances []*ecs.ContainerInstance
		pages := paginate(containerInstanceIds, 100)
		for _, page := range pages {
			input := ecs.DescribeContainerInstancesInput{
				Cluster:            &config.Conf.AwsCluster,
				ContainerInstances: page,
			}
			describeResult, err := utils.RetryThrottling(svc.DescribeContainerInstances)(&input)
			if err != nil {
				log.WithField("list", input).WithField("error", err).Error("Failed to DescribeContainerInstances!")
				continue
			}

			if len(describeResult.Failures) != 0 {
				log.WithField("result", describeResult).Error("DescribeContainerInstances Failures is not 0!")
				continue
			}

			if len(describeResult.ContainerInstances) == 0 {
				log.WithField("result", describeResult).Error("DescribeContainerInstances ContainerInstances is 0!")
				continue
			}

			containerInstances = append(containerInstances, describeResult.ContainerInstances...)

			// save to map
			instMutex.Lock()
			for _, ci := range containerInstances {
				w.containerInstances[*ci.ContainerInstanceArn] = ci
			}
			instMutex.Unlock()
		}

		// Describe all ec2 instances
		ec2InstanceMap := map[string]*string{}
		for _, ci := range containerInstances {
			if *ci.Ec2InstanceId != "" {
				ec2InstanceMap[*ci.Ec2InstanceId] = ci.Ec2InstanceId
			}
		}

		if len(ec2InstanceMap) > 0 {
			var ec2Instances []*ec2.Instance
			for {
				input := ec2.DescribeInstancesInput{
					InstanceIds: make([]*string, 0),
				}

				for _, instanceIdPtr := range ec2InstanceMap {
					if instanceIdPtr != nil {
						input.InstanceIds = append(input.InstanceIds, instanceIdPtr)
					}
				}

				ec2Result, err := utils.RetryThrottling(ec2Svc.DescribeInstances)(&input)
				if err != nil {
					log.WithField("error", err).Error("Failed to DescribeInstances!")
					break
				}

				for _, reservation := range ec2Result.Reservations {
					ec2Instances = append(ec2Instances, reservation.Instances...)
				}

				if ec2Result.NextToken != nil {
					input.NextToken = ec2Result.NextToken
				} else {
					break
				}
			}

			// save to map
			instMutex.Lock()
			for _, instance := range ec2Instances {
				w.ec2Instances[*instance.InstanceId] = instance
			}
			instMutex.Unlock()
		}
	}
}

func (w *instanceWatchWorker) getInstance(instanceId string) (*ec2.Instance, bool) {
	instMutex.RLock()
	instance, ok := w.ec2Instances[instanceId]
	instMutex.RUnlock()
	return instance, ok
}

func (w *instanceWatchWorker) getInstanceByContainerInstance(containerInstanceId string) (*ec2.Instance, bool) {
	instMutex.RLock()
	containerInstance, ok := w.containerInstances[containerInstanceId]
	instMutex.RUnlock()
	if !ok {
		return nil, false
	}

	instance, ok := w.getInstance(*containerInstance.Ec2InstanceId)
	return instance, ok
}
