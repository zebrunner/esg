package service

import (
	"context"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

func GetCapacityProviderTasks(ctx context.Context, svc *ecs.Client, capacityProviderName string) ([]ecsTypes.Task, error) {
	tasks := []ecsTypes.Task{}

	paginator := ecs.NewListTasksPaginator(svc, &ecs.ListTasksInput{
		Cluster: aws.String(config.Conf.AwsCluster),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		if len(page.TaskArns) == 0 {
			continue
		}

		describeTasksInput := &ecs.DescribeTasksInput{
			Cluster: aws.String(config.Conf.AwsCluster),
			Tasks:   page.TaskArns,
		}
		describeTasksResult, err := svc.DescribeTasks(ctx, describeTasksInput)
		if err != nil {
			log.WithError(err).Warn("Failed to get all tasks. Only partial results returned")
			break
		}

		for _, task := range describeTasksResult.Tasks {
			if task.CapacityProviderName != nil && *task.CapacityProviderName == capacityProviderName {
				tasks = append(tasks, task)
			}
		}
	}

	return tasks, nil
}

func GetClusterTasksArn(ctx context.Context, svc *ecs.Client) ([]string, error) {
	taskArns := make([]string, 0)

	paginator := ecs.NewListTasksPaginator(svc, &ecs.ListTasksInput{
		Cluster: aws.String(config.Conf.AwsCluster),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		taskArns = append(taskArns, page.TaskArns...)
	}

	return taskArns, nil
}

func GetTasksByTaskIds(ctx context.Context, taskIds []string, svc *ecs.Client) []ecsTypes.Task {
	tasks := make([]ecsTypes.Task, 0)

	pages := utils.Paginate(taskIds, 100)

	for _, tasksPage := range pages {
		describeTasksInput := &ecs.DescribeTasksInput{
			Cluster: aws.String(config.Conf.AwsCluster),
			Tasks:   tasksPage,
		}
		output, err := svc.DescribeTasks(ctx, describeTasksInput)
		if err != nil {
			log.WithError(err).Error("Failed to describe tasks!")
			continue
		}
		tasks = append(tasks, output.Tasks...)
	}

	return tasks
}

func ListContainerInstances(ctx context.Context, svc *ecs.Client) ([]string, error) {
	containerInstancesArns := make([]string, 0)

	paginator := ecs.NewListContainerInstancesPaginator(svc, &ecs.ListContainerInstancesInput{
		Cluster: aws.String(config.Conf.AwsCluster),
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			if len(containerInstancesArns) > 0 {
				return containerInstancesArns, nil
			}
			return nil, err
		}
		containerInstancesArns = append(containerInstancesArns, page.ContainerInstanceArns...)
	}

	return containerInstancesArns, nil
}

func DescribeContainerInstances(ctx context.Context, containerInstanceIds []string, svc *ecs.Client) ([]ecsTypes.ContainerInstance, error) {
	containerInstances := make([]ecsTypes.ContainerInstance, 0)

	pages := utils.Paginate(containerInstanceIds, 100)

	for _, page := range pages {
		describeInput := &ecs.DescribeContainerInstancesInput{
			Cluster:            aws.String(config.Conf.AwsCluster),
			ContainerInstances: page,
		}

		describeResult, err := svc.DescribeContainerInstances(ctx, describeInput)
		if err != nil {
			log.WithField("describeResult", describeResult).WithField("error", err).Error("Failed to DescribeContainerInstances!")
			return nil, err
		}

		if len(describeResult.Failures) != 0 {
			log.Error("Found failure in DescribeContainerInstances operation")
			for _, failure := range describeResult.Failures {
				l := log.NewEntry(log.StandardLogger())
				if failure.Arn != nil {
					l = l.WithField("Arn", *failure.Arn)
				}
				if failure.Reason != nil {
					l = l.WithField("Reason", *failure.Reason)
				}
				if failure.Detail != nil {
					l = l.WithField("Detail", *failure.Detail)
				}
				l.Error("Failure in DescribeContainerInstances")
			}
		}

		containerInstances = append(containerInstances, describeResult.ContainerInstances...)
	}

	return containerInstances, nil
}

func DescribeActiveContainerInstancesOfCapacityProvider(ctx context.Context, containerInstanceIds []string, svc *ecs.Client, capacityProviderName string) ([]ecsTypes.ContainerInstance, error) {
	ciArr, err := DescribeContainerInstances(ctx, containerInstanceIds, svc)
	if err != nil {
		return nil, err
	}

	cpCIArr := make([]ecsTypes.ContainerInstance, 0)
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

func DescribeInstances(ctx context.Context, ec2InstanceIds []string, ec2Svc *ec2.Client) ([]ec2Types.Instance, error) {
	var ec2Instances []ec2Types.Instance

	paginator := ec2.NewDescribeInstancesPaginator(ec2Svc, &ec2.DescribeInstancesInput{
		InstanceIds: ec2InstanceIds,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstances!")
			return nil, err
		}

		for _, reservation := range page.Reservations {
			ec2Instances = append(ec2Instances, reservation.Instances...)
		}
	}

	return ec2Instances, nil
}

func DescribeInstancesByAsgName(ctx context.Context, asgName string, ec2Svc *ec2.Client) ([]ec2Types.Instance, error) {
	var ec2Instances []ec2Types.Instance

	paginator := ec2.NewDescribeInstancesPaginator(ec2Svc, &ec2.DescribeInstancesInput{
		Filters: []ec2Types.Filter{{
			Name:   aws.String("tag:aws:autoscaling:groupName"),
			Values: []string{asgName},
		}},
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstances!")
			return nil, err
		}

		for _, reservation := range page.Reservations {
			ec2Instances = append(ec2Instances, reservation.Instances...)
		}
	}

	return ec2Instances, nil
}

func DescribeInstancesStatus(ctx context.Context, ec2InstanceIds []string, ec2Svc *ec2.Client) ([]string, []string, error) {
	healthyInstanceIds := make([]string, 0)
	unhealthyInstanceIds := make([]string, 0)

	paginator := ec2.NewDescribeInstanceStatusPaginator(ec2Svc, &ec2.DescribeInstanceStatusInput{
		InstanceIds: ec2InstanceIds,
	})

	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			log.WithField("error", err).Error("Failed to DescribeInstancesStatus!")
			return nil, nil, err
		}

		for _, is := range page.InstanceStatuses {
			l := log.WithField("_ec2Id", aws.ToString(is.InstanceId))

			if is.InstanceStatus.Status == ec2Types.SummaryStatusImpaired ||
				is.SystemStatus.Status == ec2Types.SummaryStatusImpaired {
				l.Error("Unhealthy instance")
				unhealthyInstanceIds = append(unhealthyInstanceIds, aws.ToString(is.InstanceId))
			} else {
				l.Trace("Healthy instance")
				healthyInstanceIds = append(healthyInstanceIds, aws.ToString(is.InstanceId))
			}
		}
	}

	return healthyInstanceIds, unhealthyInstanceIds, nil
}

func TerminateInstancesInASG(ctx context.Context, ec2InstanceIds []string, decrementDesiredCapacity bool, autoscalingSvc *autoscaling.Client) error {
	for _, instanceId := range ec2InstanceIds {
		stopInstanceInput := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     aws.String(instanceId),
			ShouldDecrementDesiredCapacity: aws.Bool(decrementDesiredCapacity),
		}

		_, err := autoscalingSvc.TerminateInstanceInAutoScalingGroup(ctx, stopInstanceInput)
		if err != nil {
			log.WithError(err).Error("Failed to terminate instance")
			return err
		}

		time.Sleep(250 * time.Millisecond)
	}

	return nil
}
