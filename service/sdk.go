package service

import (
	"math"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

func GetClusterTasks(svc *ecs.ECS) ([]*ecs.Task, error) {
	tasks := []*ecs.Task{}
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.Conf.AwsCluster,
	}
	for {
		listTasksResult, err := utils.RetryThrottling(svc.ListTasks)(listTasksInput)
		if err != nil {
			return nil, err
		}
		if len(listTasksResult.TaskArns) == 0 {
			break
		}

		describeTasksInput := &ecs.DescribeTasksInput{
			Cluster: &config.Conf.AwsCluster,
			Tasks:   listTasksResult.TaskArns,
		}
		describeTasksResult, err := utils.RetryThrottling(svc.DescribeTasks)(describeTasksInput)
		if err != nil {
			log.WithError(err).Warn("Failed to get all tasks. Only partial results returned")
			break
		}
		tasks = append(tasks, describeTasksResult.Tasks...)

		if listTasksResult.NextToken == nil {
			break
		}
		listTasksInput = listTasksInput.SetNextToken(*listTasksResult.NextToken)
	}

	return tasks, nil
}

func GetClusterTasksArn(svc *ecs.ECS) ([]*string, error) {
	taskArns := make([]*string, 0)
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.Conf.AwsCluster,
	}

	for {
		listTasksResult, err := utils.RetryThrottling(svc.ListTasks)(listTasksInput)
		if err != nil {
			return nil, err
		}
		if len(listTasksResult.TaskArns) == 0 {
			break
		}

		taskArns = append(taskArns, listTasksResult.TaskArns...)

		if listTasksResult.NextToken == nil {
			break
		}
		listTasksInput = listTasksInput.SetNextToken(*listTasksResult.NextToken)
	}

	return taskArns, nil
}

func GetSessionMapTasks(keys []string, svc *ecs.ECS) []*ecs.Task {
	// Construct pages of *string with 100 or fewer elements for requests. 100 is an AWS limitation for Describe* requests
	pages := paginate(aws.StringSlice(keys), 100)
	tasks := make([]*ecs.Task, 0)
	// Send DescribeTasks requests and save response tasks into array
	for _, tasksPage := range pages {
		if len(tasksPage) == 0 {
			break
		}

		describeTasksInput := ecs.DescribeTasksInput{
			Cluster: &config.Conf.AwsCluster,
			Tasks:   tasksPage,
		}
		output, err := utils.RetryThrottling(svc.DescribeTasks)(&describeTasksInput)
		if err != nil {
			log.WithError(err).Error("Failed to describe tasks!")
		}
		tasks = append(tasks, output.Tasks...)
	}

	return tasks
}

func DescribeContainerInstances(containerInstanceIdPtrs []*string, svc *ecs.ECS) ([]*ecs.ContainerInstance, error) {
	pages := paginate(containerInstanceIdPtrs, 100)
	containerInstances := make([]*ecs.ContainerInstance, 0)

	for _, page := range pages {
		describeInput := ecs.DescribeContainerInstancesInput{
			Cluster:            &config.Conf.AwsCluster,
			ContainerInstances: page,
		}

		describeResult, err := utils.RetryThrottling(svc.DescribeContainerInstances)(&describeInput)
		if err != nil {
			log.WithField("describeResult", describeResult).WithField("error", err).Error("Failed to DescribeContainerInstances!")
			return nil, err
		}

		log.Debug("DescribeContainerInstances failures: ", describeResult.Failures)

		containerInstances = append(containerInstances, describeResult.ContainerInstances...)
	}

	return containerInstances, nil
}

func DescribeInstances(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) ([]*ec2.Instance, error) {
	var ec2Instances []*ec2.Instance
	for {
		input := ec2.DescribeInstancesInput{
			InstanceIds: ec2InstanceIdPtrs,
		}

		ec2Result, err := utils.RetryThrottling(ec2Svc.DescribeInstances)(&input)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstances!")
			return nil, err
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

	return ec2Instances, nil
}

// DescribeInstancesStatus returns healthyInstanceIdPtrs and unhealthyInstanceIdPtrs (where InstanceStatus or SystemStatus is SummaryStatusImpaired)
// not implemented because of an error: "UnauthorizedOperation: You are not authorized to perform this operation.\n\tstatus code: 403
func DescribeInstancesStatus(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) ([]*string, []*string, error) {
	describeInput := ec2.DescribeInstanceStatusInput{
		InstanceIds: ec2InstanceIdPtrs,
	}

	healthyInstanceIdPtrs := make([]*string, 0)
	unhealthyInstanceIdPtrs := make([]*string, 0)

	for {
		statusOutput, err := utils.RetryThrottling(ec2Svc.DescribeInstanceStatus)(&describeInput)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstancesStatus!")
			return nil, nil, err
		}

		statuses := statusOutput.InstanceStatuses
		for _, instanceStatus := range statuses {
			log.Debug("instance statuses: ", *instanceStatus.InstanceStatus.Status, ", ", *instanceStatus.SystemStatus.Status)
			if *instanceStatus.InstanceStatus.Status == ec2.SummaryStatusImpaired ||
				*instanceStatus.SystemStatus.Status == ec2.SummaryStatusImpaired {
				unhealthyInstanceIdPtrs = append(unhealthyInstanceIdPtrs, instanceStatus.InstanceId)
				log.Debug("Found unhealthy instance: ", *instanceStatus.InstanceId)
			} else {
				healthyInstanceIdPtrs = append(unhealthyInstanceIdPtrs, instanceStatus.InstanceId)
			}
		}

		if statusOutput.NextToken != nil {
			describeInput.NextToken = statusOutput.NextToken
		} else {
			break
		}

	}

	return healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, nil
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
