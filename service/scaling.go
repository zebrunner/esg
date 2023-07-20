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
	"github.com/zebrunner/esg/utils"
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
		Cluster: &config.Conf.AwsCluster,
	}
	listInstancesOutput, err := utils.RetryThrottling(svc.ListContainerInstances)(listInstancesInput)
	if err != nil {
		return 0, err
	}
	instanceCount := len(listInstancesOutput.ContainerInstanceArns)
	return int64(instanceCount), nil
}

func getInstanceResources() (*Resources, error) {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		return nil, err
	}

	autoscalingSvc := autoscaling.New(session, &aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	describeGroupInput := autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(config.Conf.AwsAutoScalingGroup)},
	}
	describeGroupOutput, err := utils.RetryThrottling(autoscalingSvc.DescribeAutoScalingGroups)(&describeGroupInput)
	if err != nil {
		return nil, err
	}

	launchConfiguration := describeGroupOutput.AutoScalingGroups[0].LaunchConfigurationName
	describeLaunchConfigInput := autoscaling.DescribeLaunchConfigurationsInput{
		LaunchConfigurationNames: []*string{launchConfiguration},
	}
	result, err := utils.RetryThrottling(autoscalingSvc.DescribeLaunchConfigurations)(&describeLaunchConfigInput)
	if err != nil {
		return nil, err
	}

	instanceType := result.LaunchConfigurations[0].InstanceType
	ec2Svc := ec2.New(session, &aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	describeInstanceTypeInput := ec2.DescribeInstanceTypesInput{
		InstanceTypes: []*string{instanceType},
	}
	instanceTypesResult, err := utils.RetryThrottling(ec2Svc.DescribeInstanceTypes)(&describeInstanceTypeInput)
	if err != nil {
		return nil, err
	}

	instanceInfo := instanceTypesResult.InstanceTypes[0]

	return &Resources{CPU: *instanceInfo.VCpuInfo.DefaultVCpus * 1024, Memory: *instanceInfo.MemoryInfo.SizeInMiB}, nil
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

func setDesiredCapacity(autoscalingService *autoscaling.AutoScaling, newCapacity int64) {
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&config.Conf.AwsAutoScalingGroup},
	}
	describeAutoScalingGroupsOutput, err := utils.RetryThrottling(autoscalingService.DescribeAutoScalingGroups)(describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Failed to set desired capacity. Can't describe auto scaling group.")
		return
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]
	currentCapacity := *autoScalingGroup.DesiredCapacity
	if newCapacity < currentCapacity {
		log.WithFields(log.Fields{
			"currentCapacity": currentCapacity,
			"desiredCapacity": newCapacity,
		}).Warn("Scale down not allowed")
		return
	}

	if newCapacity > *autoScalingGroup.MaxSize {
		log.WithFields(log.Fields{
			"maxCapacity":    *autoScalingGroup.MaxSize,
			"desiredCapacity": newCapacity,
		}).Warn("ASG desired size reached limit!")
		newCapacity = *autoScalingGroup.MaxSize
	}

	if newCapacity == currentCapacity {
		// do nothing
		return
	}

	updateGroupInput := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: autoScalingGroup.AutoScalingGroupName,
		DesiredCapacity:      &newCapacity,
	}
	_, err = utils.RetryThrottling(autoscalingService.UpdateAutoScalingGroup)(updateGroupInput)
	if err != nil {
		log.WithError(err).Error("Failed to update auto scaling group")
		return
	}
	log.WithFields(log.Fields{
		"oldCapacity": currentCapacity,
		"newCapacity": newCapacity,
	}).Info("Capacity updated")
}

func ScaleUp() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		log.WithError(err).Error("Failed to create AWS session")
		return
	}
	svc := ecs.New(session)
	autoscalingSvc := autoscaling.New(session)
	tasks, err := GetClusterTasks(svc)
	if err != nil {
		log.WithError(err).Error("Failed to get list of running task")
		return
	}

	provisioningTasksResources := getTasksResources(tasks, "PROVISIONING")
	runningTasksResources := getTasksResources(tasks, "RUNNING")
	// There is no task in provisioning state, no need to scale up
	if len(provisioningTasksResources) == 0 {
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
		//TODO: [VD] review below logic. It seems pretty strange.
		// according to ZEB-6064 removed min cpu amd memory configuration and reused hardcoded values.
		if i.CPU >= int64(1024) && i.Memory >= int64(1024) {
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
	maxInstancesWithReservation := int64(math.Ceil(float64(requiredInstances) * (1 + config.Conf.ReserveInstancesPercent)))
	setDesiredCapacity(autoscalingSvc, maxInstancesWithReservation)
}

func ScaleDown() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
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

	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&config.Conf.AwsAutoScalingGroup},
	}
	describeAutoScalingGroupsOutput, err := utils.RetryThrottling(autoscalingSvc.DescribeAutoScalingGroups)(describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Can't describe auto scaling group.")
		return
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]
	minSize := *autoScalingGroup.MinSize
	desiredCapacity := *autoScalingGroup.DesiredCapacity
	currentCapacity := desiredCapacity

	instances := []*ecs.ContainerInstance{}
	listInstancesInput := ecs.ListContainerInstancesInput{
		Cluster: &config.Conf.AwsCluster,
	}
	for {
		listInstancesResult, err := utils.RetryThrottling(svc.ListContainerInstances)(&listInstancesInput)
		if err != nil && len(listInstancesResult.ContainerInstanceArns) != 0 {
			log.WithError(err).Debug("Failed to list instances")
			return
		}
		if len(listInstancesResult.ContainerInstanceArns) == 0 {
			return
		}

		log.WithField("listInstancesResult", listInstancesResult)

		containerInstances := make([]*string, 0)
		for _, containerInstanceAws := range listInstancesResult.ContainerInstanceArns {
			if containerInstanceAws != nil {
				containerInstances = append(containerInstances, containerInstanceAws)
			} else {
				log.Debug("AWS returned an empty containerInsetanceArns??")
			}
		}

		describeInstancesInput := ecs.DescribeContainerInstancesInput{
			Cluster:            &config.Conf.AwsCluster,
			ContainerInstances: containerInstances,
		}
		describeInstancesResult, err := utils.RetryThrottling(svc.DescribeContainerInstances)(&describeInstancesInput)
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
			registeredAt := time.Since(*instance.RegisteredAt)
			if registeredAt > config.Conf.InstanceCooldownTimeout {
				instancesToDelete = append(instancesToDelete, instance)
			}
		}
	}

	maxInstancesToDelete := int(math.Ceil(float64(len(instancesToDelete)) * (1 - config.Conf.ReserveInstancesPercent)))

	terminatedCount := 0
	for _, instance := range instancesToDelete {
		if desiredCapacity <= minSize {
			break
		}

		l := log.WithField("instance", *instance.Ec2InstanceId)
		if maxInstancesToDelete <= 0 {
			l.Trace("Keep instance for reservation")
			break
		}

		stopInstanceInput := autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instance.Ec2InstanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(true),
		}
		_, err := utils.RetryThrottling(autoscalingSvc.TerminateInstanceInAutoScalingGroup)(&stopInstanceInput)
		if err != nil {
			l.WithError(err).Error("Failed to stop instance")
		}
		l.Trace("Stopping instance")
		desiredCapacity -= 1
		maxInstancesToDelete -= 1
		terminatedCount++
		time.Sleep(250 * time.Millisecond)
	}
	if terminatedCount != 0 {
		log.WithFields(log.Fields{
			"oldCapacity": currentCapacity,
			"newCapacity": desiredCapacity,
		}).Info("Capacity updated")
	}
}
