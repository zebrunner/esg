package service

import (
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

func GetCapacityProviderTasks(svc *ecs.ECS, capacityProviderName string) ([]*ecs.Task, error) {
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

		for _, task := range describeTasksResult.Tasks {
			if task.CapacityProviderName != nil && *task.CapacityProviderName == capacityProviderName {
				tasks = append(tasks, task)
			}
		}

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
	pages := utils.Paginate(aws.StringSlice(taskIds), 100)

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
	pages := utils.Paginate(containerInstanceIdPtrs, 100)
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

		if describeResult.Failures != nil && len(describeResult.Failures) != 0 {
			log.Error("Found failure in DescribeContainerInstances operation")
			for _, failure := range describeResult.Failures {
				l := log.NewEntry(log.StandardLogger())
				if failure.Arn != nil {
					l = l.WithField("Arn", &failure.Arn)
				}
				if failure.Reason != nil {
					l = l.WithField("Reason", &failure.Reason)
				}
				if failure.Detail != nil {
					l = l.WithField("Detail", &failure.Detail)
				}
				l.Error("Failure in DescribeContainerInstances")
			}
		}

		containerInstances = append(containerInstances, describeResult.ContainerInstances...)
	}

	return containerInstances, nil
}

func DescribeActiveContainerInstancesOfCapacityProvider(containerInstanceIdPtrs []*string, svc *ecs.ECS, capacityProviderName string) ([]*ecs.ContainerInstance, error) {
	ciArr, err := DescribeContainerInstances(containerInstanceIdPtrs, svc)
	if err != nil {
		return nil, err
	}

	cpCIArr := make([]*ecs.ContainerInstance, 0)
	for _, containerInstance := range ciArr {
		if containerInstance.Status == nil || *containerInstance.Status != "ACTIVE" {
			continue
		}

		if containerInstance.CapacityProviderName == nil || *containerInstance.CapacityProviderName != capacityProviderName {
			continue
		}

		cpCIArr = append(cpCIArr, containerInstance)
	}

	return cpCIArr, nil
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

func DescribeInstancesByAsgName(asg *string, ec2Svc *ec2.EC2) ([]*ec2.Instance, error) {
	var ec2Instances []*ec2.Instance
	//search instances by aws:autoscaling:groupName tag
	input := ec2.DescribeInstancesInput{
		Filters: []*ec2.Filter{{
			Name:   aws.String("tag:aws:autoscaling:groupName"),
			Values: []*string{asg},
		}},
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
				l.Error("Unhealthy instance")
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
