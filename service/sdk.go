package service

import (
	"math"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
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

func GetTasksByTaskIds(taskIds []string, svc *ecs.ECS) []*ecs.Task {
	tasks := make([]*ecs.Task, 0)

	// Construct pages of *string with 100 or fewer elements for requests. 100 is an AWS limitation for Describe* requests
	pages := paginate(aws.StringSlice(taskIds), 100)

	// Send DescribeTasks requests and save response tasks into array
	for _, tasksPage := range pages {

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

func ListContainerInstances(svc *ecs.ECS) ([]*string, error) {
	containerInstancesArns := make([]*string, 0)
	listContainerInstancesInput := ecs.ListContainerInstancesInput{
		Cluster: &config.Conf.AwsCluster,
	}
	for {
		listContainerInstancesResult, err := utils.RetryThrottling(svc.ListContainerInstances)(&listContainerInstancesInput)
		if err != nil && len(listContainerInstancesResult.ContainerInstanceArns) != 0 {
			return nil, err
		}

		if len(listContainerInstancesResult.ContainerInstanceArns) == 0 {
			break
		}

		containerInstancesArns = append(containerInstancesArns, listContainerInstancesResult.ContainerInstanceArns...)

		if listContainerInstancesResult.NextToken == nil {
			break
		}
		listContainerInstancesInput = *listContainerInstancesInput.SetNextToken(*listContainerInstancesResult.NextToken)
	}

	return containerInstancesArns, nil
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

		containerInstances = append(containerInstances, describeResult.ContainerInstances...)
	}

	return containerInstances, nil
}

func DescribeInstances(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) ([]*ec2.Instance, error) {
	var ec2Instances []*ec2.Instance
	input := ec2.DescribeInstancesInput{
		InstanceIds: ec2InstanceIdPtrs,
	}
	for {
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

func DescribeInstancesStatus(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) ([]*string, []*string, error) {
	input := &ec2.DescribeInstanceStatusInput{
		InstanceIds: ec2InstanceIdPtrs,
	}

	healthyInstanceIdPtrs := make([]*string, 0)
	unhealthyInstanceIdPtrs := make([]*string, 0)

	for {
		statusOutput, err := utils.RetryThrottling(ec2Svc.DescribeInstanceStatus)(input)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstancesStatus!")
			return nil, nil, err
		}

		instanceStatuses := statusOutput.InstanceStatuses
		for _, is := range instanceStatuses {
			l := log.WithField("_ec2Id", *is.InstanceId)

			if *is.InstanceStatus.Status == ec2.SummaryStatusImpaired || *is.SystemStatus.Status == ec2.SummaryStatusImpaired {
				l.Info("Unhealthy instance")
				unhealthyInstanceIdPtrs = append(unhealthyInstanceIdPtrs, is.InstanceId)
			} else {
				l.Trace("Healthy instance")
				healthyInstanceIdPtrs = append(healthyInstanceIdPtrs, is.InstanceId)
			}
		}

		if statusOutput.NextToken != nil {
			input.NextToken = statusOutput.NextToken
		} else {
			break
		}

	}

	return healthyInstanceIdPtrs, unhealthyInstanceIdPtrs, nil
}

func TerminateInstancesInASG(ec2InstanceIdPtrs []*string, decrementDesiredCapacity bool, autoscalingSvc *autoscaling.AutoScaling) error {
	for _, instanceId := range ec2InstanceIdPtrs {
		stopInstanceInput := autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(decrementDesiredCapacity),
		}

		_, err := utils.RetryThrottling(autoscalingSvc.TerminateInstanceInAutoScalingGroup)(&stopInstanceInput)
		if err != nil {
			log.WithError(err).Error("Failed to terminate instance")
			return err
		}

		// as we terminating one by one
		time.Sleep(250 * time.Millisecond)
	}

	return nil
}

// TerminateInstances need's permissons for performing ec2Svc.TerminateInstances call
func TerminateInstances(ec2InstanceIdPtrs []*string, ec2Svc *ec2.EC2) error {
	// ec2 constraints: Up to 1000 instance IDs. We recommend breaking up this request into smaller batches
	// paginating only up to 100 instance IDs
	pages := paginate(ec2InstanceIdPtrs, 100)
	for _, page := range pages {
		input := &ec2.TerminateInstancesInput{
			InstanceIds: page,
		}

		_, err := utils.RetryThrottling(ec2Svc.TerminateInstances)(input)
		if err != nil {
			return err
		}
	}

	return nil
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
