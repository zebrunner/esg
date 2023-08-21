package service

import (
	"database/sql"
	"fmt"
	"math"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/db"
	"github.com/zebrunner/esg/utils"
)

var (
	instanceTypeResources *Resources = nil
)

type Resources struct {
	CPU    int64
	Memory int64
}

func InitScalingData() {
	var err error
	instanceTypeResources, err = getInstanceResources()
	log.Debug("Res:", *instanceTypeResources)
	if err != nil {
		log.WithError(err).Error("Failed to get instance resources. Stopping scaler")
		os.Exit(1)
	}
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

	//check actual registered resource
	go checkForResourcesChange(session, *instanceType)

	instance, err := db.GetInstance(*instanceType)
	if err != nil && err != sql.ErrNoRows {
		log.Info("err on getting instance from db: ", err)
		return nil, err
	}

	if instance != nil {
		log.Info("Found instance in db:", *instance)
		return &Resources{CPU: instance.Cpu, Memory: instance.Memory}, nil
	}
	log.Info("Didn't found instance in db")

	ec2Svc := ec2.New(session, &aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	describeInstanceTypeInput := ec2.DescribeInstanceTypesInput{
		InstanceTypes: []*string{instanceType},
	}
	instanceTypesResult, err := utils.RetryThrottling(ec2Svc.DescribeInstanceTypes)(&describeInstanceTypeInput)
	if err != nil {
		return nil, err
	}
	instanceInfo := instanceTypesResult.InstanceTypes[0]

	//considering that 5% of total memory is used by an instance system needs
	executableMemoryPercent := 0.95
	registeredMemoryToUse := int64(float64(*instanceInfo.MemoryInfo.SizeInMiB) * executableMemoryPercent)

	return &Resources{CPU: *instanceInfo.VCpuInfo.DefaultVCpus * 1024, Memory: registeredMemoryToUse}, nil
}

//works only if one instance type used among all build
func checkForResourcesChange(session *awsSession.Session, instanceType string) {
	//perform check every periodBeforeCheck time
	periodBeforeCheck := time.Hour * 12
	//if failed to get registerd resources, trying to get it after periodAfterFailCheck time
	//the most common reason to fail = 0 instances is up
	periodAfterFailCheck := time.Minute * 5

	svc := ecs.New(session)
	//Check if any changes happend
	for {
		listInstancesInput := &ecs.ListContainerInstancesInput{
			Cluster: &config.Conf.AwsCluster,
		}

		listInstancesOutput, err := utils.RetryThrottling(svc.ListContainerInstances)(listInstancesInput)

		if err != nil || len(listInstancesOutput.ContainerInstanceArns) == 0 {
			log.WithError(err).Info("no ContainerInstance were found")
			time.Sleep(periodAfterFailCheck)
			continue
		}

		ciArr, err := DescribeContainerInstances([]*string{listInstancesOutput.ContainerInstanceArns[0]}, svc)
		if err != nil {
			log.WithError(err).Info("Error on describing ci")
			time.Sleep(periodAfterFailCheck)
			continue
		}

		currentResources := &Resources{}
		ci := ciArr[0]
		for _, resource := range ci.RegisteredResources {
			if *resource.Name == "CPU" {
				currentResources.CPU = *resource.IntegerValue
			} else if *resource.Name == "MEMORY" {
				currentResources.Memory = *resource.IntegerValue
			}
		}
		log.Info("Registered resources: ", *currentResources)

		if currentResources.CPU == 0 || currentResources.Memory == 0 {
			time.Sleep(periodAfterFailCheck)
			continue
		}

		instance, err := db.GetInstance(instanceType)
		if err != nil {
			if err == sql.ErrNoRows {
				instance := &db.Instance{
					Type:   instanceType,
					Cpu:    currentResources.CPU,
					Memory: currentResources.Memory,
				}
				db.CreateInstance(instance)
				log.Info("Created instance record in db: ", *instance)
				instanceTypeResources = currentResources
			} else {
				log.Info("err on getting instance from db: ", err)
				time.Sleep(periodAfterFailCheck)
				continue
			}
		} else {
			if instance.Cpu != currentResources.CPU || instance.Memory != currentResources.Memory {
				log.WithFields(log.Fields{"instance: ": *instance, "current resources: ": *currentResources}).Info("instance record differs from registered resources", err)
				instance.Cpu = currentResources.CPU
				instance.Memory = currentResources.Memory
				db.RefreshInstance(instance)
				log.Info("Updated instance record in db: ", *instance)
				instanceTypeResources = currentResources
			} else {
				log.Info("No diff in resources were found ", *instance)
			}
		}

		time.Sleep(periodBeforeCheck)
	}
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

func getAutoscalingGroup(autoscalingSvc *autoscaling.AutoScaling) (*autoscaling.Group, error) {
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&config.Conf.AwsAutoScalingGroup},
	}
	describeAutoScalingGroupsOutput, err := utils.RetryThrottling(autoscalingSvc.DescribeAutoScalingGroups)(describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Failed to describe auto scaling group.")
		return nil, err
	}

	if len(describeAutoScalingGroupsOutput.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("autoscaling group with name %s not found", config.Conf.AwsAutoScalingGroup)
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]

	return autoScalingGroup, nil
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
	// There is no task in provisioning state, no need to scale up
	if len(provisioningTasksResources) == 0 {
		return
	}

	asg, err := getAutoscalingGroup(autoscalingSvc)
	if err != nil {
		log.WithError(err).Error("Failed to get autoscaling group")
		return
	}
	currentCapacity := *asg.DesiredCapacity

	//copying to variable, as instanceTypeResources could be changed in runtime
	instanceType := *instanceTypeResources

	// Generate list of resources for each instance
	instanceResources := make([]*Resources, 0, int(currentCapacity))
	for i := 0; i < int(currentCapacity); i++ {
		instanceResources = append(instanceResources, &Resources{
			CPU:    instanceType.CPU,
			Memory: instanceType.Memory,
		})
	}

	runningTasksResources := getTasksResources(tasks, "RUNNING")
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

	// Remove resources that might be used for PROVISSIONING tasks
	requiredTaskResources := []*Resources{}
	for _, t := range provisioningTasksResources {
		enough := false
		for _, i := range instanceResources {
			if i.CPU >= t.CPU && i.Memory >= t.Memory {
				i.CPU -= t.CPU
				i.Memory -= t.Memory
				enough = true
				break
			}
		}

		if !enough {
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

	requiredCpu := float64(totalRequiredResources.CPU) / float64(instanceType.CPU)
	requiredMemory := float64(totalRequiredResources.Memory) / float64(instanceType.Memory)

	desiredCapacity := float64(currentCapacity) + math.Max(requiredCpu, requiredMemory)
	desiredReservationCapacity := desiredCapacity * (1 + config.Conf.ReserveInstancesPercent)

	if desiredReservationCapacity-desiredCapacity > float64(config.Conf.ReserveMaxCapacity) {
		log.WithFields(log.Fields{
			"desired reservation capacity": math.Ceil(desiredReservationCapacity),
			"desired capacity":             math.Ceil(desiredCapacity),
			"max reservation capacity":     config.Conf.ReserveMaxCapacity,
		}).Warn("Triggered max reservation capacity limit")
		desiredReservationCapacity = desiredCapacity + float64(config.Conf.ReserveMaxCapacity)
	}

	newCapacity := int64(math.Ceil(desiredReservationCapacity))

	log.Debug("requiredCpu:", requiredCpu, " requiredMemory:", requiredMemory, " requiredInstances:", desiredCapacity, " withReservation:", desiredReservationCapacity)

	if newCapacity > *asg.MaxSize {
		log.WithFields(log.Fields{
			"maxCapacity":     *asg.MaxSize,
			"desiredCapacity": newCapacity,
		}).Warn("ASG desired size reached limit!")
		newCapacity = *asg.MaxSize
	}

	if newCapacity == *asg.DesiredCapacity {
		// do nothing
		return
	}

	updateGroupInput := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: asg.AutoScalingGroupName,
		DesiredCapacity:      &newCapacity,
	}
	_, err = utils.RetryThrottling(autoscalingSvc.UpdateAutoScalingGroup)(updateGroupInput)
	if err != nil {
		log.WithError(err).Error("Failed to update auto scaling group")
		return
	}
	log.WithFields(log.Fields{
		"oldCapacity": currentCapacity,
		"newCapacity": newCapacity,
	}).Info("Capacity updated")
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

	asg, err := getAutoscalingGroup(autoscalingSvc)
	if err != nil {
		log.WithError(err).Error("Failed to get autoscaling group")
		return
	}
	minSize := *asg.MinSize
	newCapacity, currentCapacity := *asg.DesiredCapacity, *asg.DesiredCapacity

	ciArns, err := ListContainerInstances(svc)
	if err != nil {
		log.WithError(err).Debug("Failed to list container instances")
		return
	}

	containerInstances, err := DescribeContainerInstances(ciArns, svc)
	if err != nil {
		log.WithError(err).Error("Failed to describe container instances")
		return
	}

	instancesToDelete := make([]*ecs.ContainerInstance, 0)
	for _, instance := range containerInstances {
		if *instance.PendingTasksCount == 0 && *instance.RunningTasksCount == 0 {
			instanceUptime := time.Since(*instance.RegisteredAt)
			if instanceUptime > config.Conf.InstanceCooldownTimeout {
				instancesToDelete = append(instancesToDelete, instance)
			}
		}
	}

	instanceToDeleteReserved := float64(len(instancesToDelete)) * (1 - config.Conf.ReserveInstancesPercent)
	if float64(len(instancesToDelete))-instanceToDeleteReserved > float64(config.Conf.ReserveMaxCapacity) {
		log.WithFields(log.Fields{
			"instances to delete":                 len(instancesToDelete),
			"instances to delete except reserved": math.Ceil(instanceToDeleteReserved),
			"max reservation capacity":            config.Conf.ReserveMaxCapacity,
		}).Warn("Triggered max reservation capacity limit")
		instanceToDeleteReserved = float64(int64(len(instancesToDelete)) - config.Conf.ReserveMaxCapacity)
	}

	maxInstancesToDelete := int(math.Ceil(instanceToDeleteReserved))

	terminatedCount := 0
	for _, instance := range instancesToDelete {
		if newCapacity <= minSize || maxInstancesToDelete <= 0 {
			break
		}

		l := log.WithField("instance", *instance.Ec2InstanceId)

		l.Trace("Stopping instance")
		stopInstanceInput := autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instance.Ec2InstanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(true),
		}
		_, err := utils.RetryThrottling(autoscalingSvc.TerminateInstanceInAutoScalingGroup)(&stopInstanceInput)
		if err != nil {
			l.WithError(err).Error("Failed to stop instance")
		}

		newCapacity -= 1
		maxInstancesToDelete -= 1
		terminatedCount++
		time.Sleep(250 * time.Millisecond)
	}
	if terminatedCount != 0 {
		log.WithFields(log.Fields{
			"oldCapacity": currentCapacity,
			"newCapacity": newCapacity,
		}).Info("Capacity updated")
	}
}
