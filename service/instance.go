package service

import (
	"math"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/config"

        log "github.com/sirupsen/logrus"
)

var instanceWorker *instanceWatchWorker

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
			listResult, err := svc.ListContainerInstances(&listInput)
			if err != nil {
				log.WithField(err).Error("Failed to ListContainerInstances!")
				break
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
			describeResult, err := svc.DescribeContainerInstances(&input)
			if err != nil {
                                log.WithField(err).Error("Failed to DescribeContainerInstances!")
				continue
			}

			if len(describeResult.Failures) != 0 {
                               log.WithField(describeResult).Error("DescribeContainerInstances Failures is not 0!")
				continue
			}

			if len(describeResult.ContainerInstances) == 0 {
                                log.WithField(describeResult).Error("DescribeContainerInstances ContainerInstances is 0!")
				continue
			}

			containerInstances = append(containerInstances, describeResult.ContainerInstances...)

			// save to map
			for _, ci := range containerInstances {
				w.containerInstances[*ci.ContainerInstanceArn] = ci
			}
		}

		// Describe all ec2 instances
		ec2InstanceMap := map[string]string{}
		for _, ci := range containerInstances {
			if *ci.Ec2InstanceId != "" {
				ec2InstanceMap[*ci.Ec2InstanceId] = *ci.Ec2InstanceId
			}
		}

		ec2InstanceIds := []string{}
		for id := range ec2InstanceMap {
			ec2InstanceIds = append(ec2InstanceIds, id)
		}

		ec2InstanceIdsPtrs := []*string{}
		for _, id := range ec2InstanceIds {
			instanceId := id
			ec2InstanceIdsPtrs = append(ec2InstanceIdsPtrs, &instanceId)
		}

		var ec2Instances []*ec2.Instance
		for {
			input := ec2.DescribeInstancesInput{
				InstanceIds: ec2InstanceIdsPtrs,
			}

			ec2Result, err := ec2Svc.DescribeInstances(&input)
			if err != nil {
                                log.WithField(err).Error("Failed to DescribeInstances!")
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
		for _, instance := range ec2Instances {
			w.ec2Instances[*instance.InstanceId] = instance
		}
	}
}

func (w *instanceWatchWorker) getInstance(instanceId string) (*ec2.Instance, bool) {
	instance, ok := w.ec2Instances[instanceId]
	return instance, ok
}

func (w *instanceWatchWorker) getInstanceByContainerInstance(containerInstanceId string) (*ec2.Instance, bool) {
	containerInstance, ok := w.containerInstances[containerInstanceId]
	if !ok {
		return nil, false
	}

	instance, ok := w.ec2Instances[*containerInstance.Ec2InstanceId]
	return instance, ok
}
