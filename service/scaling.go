package service

import (
	"errors"
	"math"
	"strconv"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

type Resources struct {
	CPU    int64
	Memory int64
}

type ClusterResources struct {
	CurrentResources      Resources
	ReservedResources     Resources
	ProvisioningResources Resources
}

func getInstanceCount(svc *ecs.ECS) (int64, error) {
	listInstancesInput := &ecs.ListContainerInstancesInput{
		Cluster: &config.AwsCluster,
	}
	listInstancesOutput, err := svc.ListContainerInstances(listInstancesInput)
	if err != nil {
		return 0, err
	}
	instanceCount := len(listInstancesOutput.ContainerInstanceArns)
	return int64(instanceCount), nil
}

func getInstanceResources() (*Resources, error) {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.AwsRegion, MaxRetries: &config.AwsRetry})
	if err != nil {
		return nil, err
	}

	autoscalingSvc := autoscaling.New(session, &aws.Config{Region: &config.AwsRegion, MaxRetries: &config.AwsRetry})
	describeGroupInput := autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(config.AwsAutoScalingGroup)},
	}
	describeGroupOutput, err := autoscalingSvc.DescribeAutoScalingGroups(&describeGroupInput)
	if err != nil {
		return nil, err
	}

	launchConfiguration := describeGroupOutput.AutoScalingGroups[0].LaunchConfigurationName
	describeLaunchConfigInput := autoscaling.DescribeLaunchConfigurationsInput{
		LaunchConfigurationNames: []*string{launchConfiguration},
	}
	result, err := autoscalingSvc.DescribeLaunchConfigurations(&describeLaunchConfigInput)
	if err != nil {
		return nil, err
	}

	instanceType := result.LaunchConfigurations[0].InstanceType
	ec2Svc := ec2.New(session, &aws.Config{Region: &config.AwsRegion, MaxRetries: &config.AwsRetry})
	describeInstanceTypeInput := ec2.DescribeInstanceTypesInput{
		InstanceTypes: []*string{instanceType},
	}
	instanceTypesResult, err := ec2Svc.DescribeInstanceTypes(&describeInstanceTypeInput)
	if err != nil {
		return nil, err
	}

	instanceInfo := instanceTypesResult.InstanceTypes[0]

	return &Resources{CPU: *instanceInfo.VCpuInfo.DefaultCores * 1024, Memory: *instanceInfo.MemoryInfo.SizeInMiB}, nil
}

func getTasks(svc *ecs.ECS) ([]*ecs.Task, error) {
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.AwsCluster,
	}
	listTasksResult, err := svc.ListTasks(listTasksInput)
	if err != nil {
		return nil, err
	}
	if len(listTasksResult.TaskArns) == 0 {
		return nil, errors.New("can't describe tasks. List of tasks is empty")
	}
	describeTasksInput := &ecs.DescribeTasksInput{
		Cluster: &config.AwsCluster,
		Tasks:   listTasksResult.TaskArns,
	}
	describeTasksResult, err := svc.DescribeTasks(describeTasksInput)
	if err != nil {
		return nil, err
	}

	return describeTasksResult.Tasks, nil
}

func getTasksResources(tasks []*ecs.Task, status string) Resources {
	resources := Resources{
		CPU:    0,
		Memory: 0,
	}
	for _, task := range tasks {
		taskCpu, cpuErr := strconv.Atoi(*task.Cpu)
		taskMemory, memoryErr := strconv.Atoi(*task.Memory)
		if *task.LastStatus == status && cpuErr == nil && memoryErr == nil {
			resources.CPU += int64(taskCpu)
			resources.Memory += int64(taskMemory)
		}
	}

	return resources
}

func setDesiredCapacity(autoscalingService *autoscaling.AutoScaling, newDesiredCapacity int64) {
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&config.AwsAutoScalingGroup},
	}
	describeAutoScalingGroupsOutput, err := autoscalingService.DescribeAutoScalingGroups(describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Failed to set desired capacity. Can't describe auto scaling group.")
		return
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]
	if newDesiredCapacity < *autoScalingGroup.DesiredCapacity {
		log.WithFields(log.Fields{
			"currentCapacity": *autoScalingGroup.DesiredCapacity,
			"newCapacity":     newDesiredCapacity,
		}).Warn("Scale down not allowed")
		return
	}

	if newDesiredCapacity > *autoScalingGroup.MaxSize {
		log.WithFields(log.Fields{
			"maxCount":    *autoScalingGroup.MaxSize,
			"newCapacity": newDesiredCapacity,
		}).Warn("ASG desired size reached limit!")
		newDesiredCapacity = *autoScalingGroup.MaxSize
	}
	updateGroupInput := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: autoScalingGroup.AutoScalingGroupName,
		DesiredCapacity:      &newDesiredCapacity,
	}
	_, err = autoscalingService.UpdateAutoScalingGroup(updateGroupInput)
	if err != nil {
		log.WithError(err).Error("Failed to update auto scaling group")
		return
	}
	log.WithFields(log.Fields{
		"currentCapacity": *autoScalingGroup.DesiredCapacity,
		"newCapacity":     newDesiredCapacity,
	}).Debug("Capacity updated")
}

func ScaleUp() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.AwsRegion, MaxRetries: &config.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session")
		return
	}
	svc := ecs.New(session)
	autoscalingSvc := autoscaling.New(session)
	tasks, err := getTasks(svc)
	if err != nil {
		log.WithError(err).Error("Failed to get list of running task")
		return
	}

	provisioningTasksResources := getTasksResources(tasks, "PROVISIONING")
	// All tasks is running
	if provisioningTasksResources.CPU == 0 && provisioningTasksResources.Memory == 0 {
		log.Debug("There is no tasks in PROVISIONING state. No need to scale up")
		return
	}

	instanceResources, err := getInstanceResources()
	if err != nil {
		log.WithError(err).Error("Failed to get instance resources")
		return
	}

	cpuRatio := float64(provisioningTasksResources.CPU) / float64(instanceResources.CPU)
	memoryRatio := float64(provisioningTasksResources.Memory) / float64(instanceResources.Memory)

	curentInstanceCount, err := getInstanceCount(svc)
	if err != nil {
		log.WithError(err).Error("Failed to get instance count")
		return
	}
	requiredInstances := curentInstanceCount + int64(math.Ceil(math.Max(cpuRatio, memoryRatio)))
	setDesiredCapacity(autoscalingSvc, requiredInstances)
}
