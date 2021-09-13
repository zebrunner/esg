package service

import (
	"math"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	instanceTypeResources *Resources = nil
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

	return &Resources{CPU: *instanceInfo.VCpuInfo.DefaultVCpus * 1024, Memory: *instanceInfo.MemoryInfo.SizeInMiB}, nil
}

func getTasks(svc *ecs.ECS) ([]*ecs.Task, error) {
	tasks := []*ecs.Task{}
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.AwsCluster,
	}
	for {
		listTasksResult, err := svc.ListTasks(listTasksInput)
		if err != nil {
			return nil, err
		}
		if len(listTasksResult.TaskArns) == 0 {
			break
		}

		describeTasksInput := &ecs.DescribeTasksInput{
			Cluster: &config.AwsCluster,
			Tasks:   listTasksResult.TaskArns,
		}
		describeTasksResult, err := svc.DescribeTasks(describeTasksInput)
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

func getTasksResources(tasks []*ecs.Task, status string) []*Resources {
	resources := []*Resources{}
	for _, task := range tasks {
		taskCpu, cpuErr := strconv.Atoi(*task.Cpu)
		taskMemory, memoryErr := strconv.Atoi(*task.Memory)
		if *task.LastStatus == status && cpuErr == nil && memoryErr == nil {
			resources = append(resources, &Resources{
				CPU:    int64(taskCpu),
				Memory: int64(taskMemory),
			})
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
	}).Info("Capacity updated")
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
	runningTasksResources := getTasksResources(tasks, "RUNNING")
	// There is no task in provisioning state, no need to scale up
	if len(provisioningTasksResources) == 0 {
		log.Trace("There is no task in provisioning state, no need to scale up")
		return
	}

	if instanceTypeResources == nil {
		instanceTypeResources, err = getInstanceResources()
		if err != nil {
			log.WithError(err).Error("Failed to get instance resources")
			return
		}
	}

	currentInstanceCount, err := getInstanceCount(svc)
	if err != nil {
		log.WithError(err).Error("Failed to get instance count")
		return
	}

	// Generate list of resources for each instance
	instanceResources := []*Resources{}
	for i := 0; i < int(currentInstanceCount); i++ {
		instanceResources = append(instanceResources, &Resources{
			CPU:    instanceTypeResources.CPU,
			Memory: instanceTypeResources.Memory,
		})
	}

	// Remove resources that already are using by RUNNING tasks
	for _, t := range runningTasksResources {
		for _, i := range instanceResources {
			if i.CPU >= t.CPU && i.Memory >= t.Memory {
				i.CPU -= t.CPU
				i.Memory -= t.Memory
				break
			}
		}
	}

	// Remove all instances that couldn't run a tasks
	freeInstanceResources := []*Resources{}
	for _, i := range instanceResources {
		if i.CPU >= int64(config.MinCpu) && i.Memory >= int64(config.MinMemory) {
			freeInstanceResources = append(freeInstanceResources, i)
		}
	}

	// Remove resources that might be used for PROVISSIONING tasks
	enought := false
	requiredTaskResources := []*Resources{}
	for _, t := range provisioningTasksResources {
		enought = false
		for _, i := range freeInstanceResources {
			if i.CPU >= t.CPU && i.Memory >= t.Memory {
				i.CPU -= t.CPU
				i.Memory -= t.Memory
				enought = true
				break
			}
		}

		if !enought {
			requiredTaskResources = append(requiredTaskResources, t)
		}
	}

	// No new resources required right now
	if len(requiredTaskResources) == 0 {
		log.Trace("No new resources required")
		return
	}

	totalRequiredResources := Resources{
		CPU:    0,
		Memory: 0,
	}
	for _, t := range requiredTaskResources {
		totalRequiredResources.CPU += t.CPU
		totalRequiredResources.Memory += t.Memory
	}
	log.WithFields(log.Fields{
		"CPU":    totalRequiredResources.CPU,
		"Memory": totalRequiredResources.Memory,
	}).Debug("Total required resources")

	requiredCpu := float64(totalRequiredResources.CPU) / float64(instanceTypeResources.CPU)
	requiredMemory := float64(totalRequiredResources.Memory) / float64(instanceTypeResources.Memory)

	requiredInstances := currentInstanceCount + int64(math.Ceil(math.Max(requiredCpu, requiredMemory)))
	setDesiredCapacity(autoscalingSvc, requiredInstances)
}

func ScaleDown() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.AwsRegion, MaxRetries: &config.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session")
		return
	}
	svc := ecs.New(session)
	autoscalingSvc := autoscaling.New(session)
	if err != nil {
		log.WithError(err).Error("Failed to get list of running task")
		return
	}

	instances := []*ecs.ContainerInstance{}
	listInstancesInput := ecs.ListContainerInstancesInput{
		Cluster: &config.AwsCluster,
	}
	for {
		listInstancesResult, err := svc.ListContainerInstances(&listInstancesInput)
		if err != nil || len(listInstancesResult.ContainerInstanceArns) == 0 {
			log.WithError(err).WithField("count", len(listInstancesResult.ContainerInstanceArns)).Error("Failed to list instances")
			return
		}

		describeInstancesInput := ecs.DescribeContainerInstancesInput{
			Cluster:            &config.AwsCluster,
			ContainerInstances: listInstancesResult.ContainerInstanceArns,
		}
		describeInstancesResult, err := svc.DescribeContainerInstances(&describeInstancesInput)
		if err != nil {
			log.WithError(err).Error("Failed to describe instances")
			break
		}

		instances = append(instances, describeInstancesResult.ContainerInstances...)
		if listInstancesResult.NextToken != nil {
			listInstancesInput.NextToken = listInstancesResult.NextToken
		} else {
			break
		}
	}

	instancesToDelete := []*ecs.ContainerInstance{}
	for _, instance := range instances {
		if *instance.PendingTasksCount == 0 && *instance.RunningTasksCount == 0 {
			instancesToDelete = append(instancesToDelete, instance)
		}
	}

	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&config.AwsAutoScalingGroup},
	}
	describeAutoScalingGroupsOutput, err := autoscalingSvc.DescribeAutoScalingGroups(describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Failed to set desired capacity. Can't describe auto scaling group.")
		return
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]
	minSize := *autoScalingGroup.MinSize
	desiredCapacity := *autoScalingGroup.DesiredCapacity

	reserve := int(math.Ceil(float64(len(instancesToDelete)) * (1 - config.ReserveInstancesPercent)))
	if reserve < len(instancesToDelete) {
		instancesToDelete = instancesToDelete[:reserve+1]
	}

	for _, instance := range instancesToDelete {
		if desiredCapacity <= minSize {
			break
		}

		stopInstanceInput := autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instance.Ec2InstanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(true),
		}
		_, err := autoscalingSvc.TerminateInstanceInAutoScalingGroup(&stopInstanceInput)
		if err != nil {
			log.WithError(err).WithField("instance", *instance.Ec2InstanceId).Error("Failed to stop instance")
		}
		log.WithField("instance", *instance.Ec2InstanceId).Trace("Stopping instance")
		desiredCapacity -= 1
		time.Sleep(250 * time.Millisecond)
	}
}
