package service

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/resourcesToAllocate"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

const (
	LINUX_SERVICE_EXPORTER = "linux-exporter"
)

type scaler struct {
	InstanceResources    Resources
	CapacityProviderName string
	autoscalingGroupName string
	resourcesPerWeight   Resources
	instanceMinWeight    int64
	exporterResources    *Resources
	log                  *log.Entry
}

type Resources struct {
	CPU    int64
	Memory int64
}

func StartScalers(scalersMap map[string]scaler) {
	for _, s := range scalersMap {
		go func(s scaler) {
			for {
				time.Sleep(15 * time.Second)
				s.AdjustCapacity()
			}
		}(s)

		go func(s scaler) {
			for {
				time.Sleep(10 * time.Minute)
				s.StopEc2ZombieInstances()
			}
		}(s)

		log.WithFields(log.Fields{"instanceResources": s.InstanceResources, "minInstanceWeight": s.instanceMinWeight, "capacityProvider": s.CapacityProviderName, "asg": s.autoscalingGroupName}).Info("Started scaler")
	}
}

func InitScalingData() (map[string]scaler, error) {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		return nil, err
	}

	ecsSvc := ecs.New(session)
	describeClusterInput := ecs.DescribeClustersInput{
		Clusters: []*string{&config.Conf.AwsCluster},
	}
	describeClusterOutput, err := utils.RetryThrottling(ecsSvc.DescribeClusters)(&describeClusterInput)
	if err != nil {
		return nil, err
	} else if len(describeClusterOutput.Clusters) == 0 {
		return nil, fmt.Errorf("failed to describe cluster: %s", config.Conf.AwsCluster)
	}

	describeCapacityProvidersInput := ecs.DescribeCapacityProvidersInput{
		CapacityProviders: describeClusterOutput.Clusters[0].CapacityProviders,
	}
	describeCapacityProvidersOutput, err := utils.RetryThrottling(ecsSvc.DescribeCapacityProviders)(&describeCapacityProvidersInput)
	if err != nil {
		return nil, err
	} else if len(describeCapacityProvidersOutput.CapacityProviders) == 0 {
		return nil, fmt.Errorf("failed to describe capacity providers")
	}

	scalers := make(map[string]scaler)
	for _, capacityProvider := range describeCapacityProvidersOutput.CapacityProviders {
		asgArn := capacityProvider.AutoScalingGroupProvider.AutoScalingGroupArn
		asgArnSplited := strings.Split(*asgArn, "/")
		asgName := asgArnSplited[len(asgArnSplited)-1]

		autoscalingSvc := autoscaling.New(session)
		describeGroupInput := autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []*string{aws.String(asgName)},
		}
		describeGroupOutput, err := utils.RetryThrottling(autoscalingSvc.DescribeAutoScalingGroups)(&describeGroupInput)
		if err != nil {
			return nil, err
		}

		instanceOverrides := describeGroupOutput.AutoScalingGroups[0].MixedInstancesPolicy.LaunchTemplate.Overrides
		if len(instanceOverrides) <= 0 {
			return nil, fmt.Errorf("no instances were provided in MixedInstancesPolicy")
		}

		// From doc: Value must be in the range of 1 to 999.
		instanceWithMinWeight := &autoscaling.LaunchTemplateOverrides{
			WeightedCapacity: aws.String("1000"),
		}
		var minWeight int64 = 1000
		for i := 0; i < len(instanceOverrides); i++ {
			if instanceOverrides[i].WeightedCapacity == nil {
				return nil, fmt.Errorf("every instance in MixedInstancesPolicy should have its own weight")
			}

			weight, _ := strconv.ParseInt(*instanceOverrides[i].WeightedCapacity, 10, 64)
			if weight < minWeight {
				minWeight = weight
				instanceWithMinWeight = instanceOverrides[i]
			}
		}

		ec2Svc := ec2.New(session)
		describeInstanceTypeInput := ec2.DescribeInstanceTypesInput{
			InstanceTypes: []*string{instanceWithMinWeight.InstanceType},
		}
		instanceTypesResult, err := utils.RetryThrottling(ec2Svc.DescribeInstanceTypes)(&describeInstanceTypeInput)
		if err != nil {
			return nil, err
		}

		instanceInfo := instanceTypesResult.InstanceTypes[0]

		var exporterResources *Resources
		service, err := DescribeService(LINUX_SERVICE_EXPORTER)
		if err != nil {
			log.WithError(err).Warnf("Failed to describe %s service", LINUX_SERVICE_EXPORTER)
		} else if service != nil && service.TaskDefinition != nil {
			taskDef, err := DescribeTaskDefinition(*service.TaskDefinition)
			if err != nil {
				log.WithError(err).Warnf("Failed to describe %s task definition", *service.TaskDefinition)
			} else {
				exporterResources = &Resources{0, 0}

				if taskDef.Cpu != nil {
					exporterResources.CPU, _ = strconv.ParseInt(*taskDef.Cpu, 10, 64)
				}

				if taskDef.Memory != nil {
					exporterResources.Memory, _ = strconv.ParseInt(*taskDef.Memory, 10, 64)
				}
			}
		}

		s := scaler{
			CapacityProviderName: *capacityProvider.Name,
			autoscalingGroupName: asgName,
			resourcesPerWeight:   Resources{CPU: (*instanceInfo.VCpuInfo.DefaultVCpus * 1024) / minWeight, Memory: (*instanceInfo.MemoryInfo.SizeInMiB) / minWeight},
			InstanceResources:    Resources{CPU: *instanceInfo.VCpuInfo.DefaultVCpus * 1024, Memory: *instanceInfo.MemoryInfo.SizeInMiB},
			exporterResources:    exporterResources,
			instanceMinWeight:    minWeight,
			log:                  log.WithFields(log.Fields{"asg": asgName, "capacityProvider": *capacityProvider.Name}),
		}

		scalers[s.CapacityProviderName] = s
	}

	return scalers, nil
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

func (s *scaler) generateEmptyResources(capacity int) []*Resources {
	resources := make([]*Resources, 0)
	var i int64 = int64(capacity)
	for ; i >= s.instanceMinWeight; i -= s.instanceMinWeight {
		resources = append(resources, &Resources{
			CPU:    s.InstanceResources.CPU,
			Memory: s.InstanceResources.Memory,
		})
	}

	if i > 0 {
		k := float64(i) / float64(s.instanceMinWeight)
		resources = append(resources, &Resources{
			CPU:    int64(float64(s.resourcesPerWeight.CPU) * k),
			Memory: int64(float64(s.resourcesPerWeight.Memory) * k),
		})
	}

	return resources
}

func (s *scaler) calculateResources(tasks []*ecs.Task, currentCapacity int, statuses ...string) (freeResources []*Resources, requiredResources []*Resources) {
	// Generate list of resources for each instance
	freeResources = s.generateEmptyResources(currentCapacity)

	requiredResources = make([]*Resources, 0)
	for _, status := range statuses {
		tasksResourcesInUse := getTasksResources(tasks, status)
		// Remove resources that already are using by tasks with passed status
		for _, t := range tasksResourcesInUse {
			enough := false
			for _, i := range freeResources {
				if i.CPU >= t.CPU && i.Memory >= t.Memory {
					i.CPU -= t.CPU
					i.Memory -= t.Memory
					enough = true
					break
				}
			}

			if !enough {
				// Add resources that cannot be placed on any instance
				requiredResources = append(requiredResources, t)
			}
		}
	}

	return freeResources, requiredResources
}

func (s *scaler) getResourcesForAllocation() []*resourcesToAllocate.ResourcesToAllocate {
	resources, err := resourcesToAllocate.GetEntitiesOfCapacityProvider(s.CapacityProviderName)
	if err != nil {
		log.WithError(err).Error("Failed to get resources for allocation")
		return nil
	}

	return resources
}

func (s *scaler) getInstancesInAutoscalingGroup(autoscalingSvc *autoscaling.AutoScaling) (map[string]*autoscaling.InstanceDetails, error) {
	instancesMap := make(map[string]*autoscaling.InstanceDetails)
	instanceIdsArr := make([]*string, 0)
	describeInstancesInput := autoscaling.DescribeAutoScalingInstancesInput{}
	for {
		describeInstancesOutput, err := utils.RetryThrottling(autoscalingSvc.DescribeAutoScalingInstances)(&describeInstancesInput)
		if err != nil {
			return nil, err
		}

		for _, instance := range describeInstancesOutput.AutoScalingInstances {
			if instance.AutoScalingGroupName == nil || *instance.AutoScalingGroupName != s.autoscalingGroupName {
				continue
			}

			instancesMap[*instance.InstanceId] = instance
			instanceIdsArr = append(instanceIdsArr, instance.InstanceId)
		}

		if describeInstancesOutput.NextToken == nil {
			break
		}

		describeInstancesInput.NextToken = describeInstancesOutput.NextToken
	}

	if len(instanceIdsArr) == 0 {
		return instancesMap, nil
	}

	ec2Svc := ec2.New(AwsSess)
	ec2Instances, err := DescribeInstances(instanceIdsArr, ec2Svc)
	if err != nil {
		return nil, err
	}

	// filter by ec2 instance state and scale-in protection
	for _, ec2Instance := range ec2Instances {
		if ec2Instance.State.Code == nil {
			delete(instancesMap, *ec2Instance.InstanceId)
			continue
		}

		// 0 - pending
		if *ec2Instance.State.Code == 0 {
			continue
		}

		// 16 - running
		if *ec2Instance.State.Code == 16 {
			isProtected := false
			var details *autoscaling.InstanceDetails
			if details, isProtected = instancesMap[*ec2Instance.InstanceId]; isProtected {
				isProtected = *details.ProtectedFromScaleIn
			}

			if !isProtected {
				delete(instancesMap, *ec2Instance.InstanceId)
			}

			continue
		}

		delete(instancesMap, *ec2Instance.InstanceId)
	}

	return instancesMap, nil
}

func (s *scaler) getAutoscalingGroup(autoscalingSvc *autoscaling.AutoScaling) (*autoscaling.Group, error) {
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&s.autoscalingGroupName},
	}
	describeAutoScalingGroupsOutput, err := utils.RetryThrottling(autoscalingSvc.DescribeAutoScalingGroups)(describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Failed to describe auto scaling group.")
		return nil, err
	}

	if len(describeAutoScalingGroupsOutput.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("autoscaling group with name %s not found", s.autoscalingGroupName)
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]

	return autoScalingGroup, nil
}

func (s *scaler) AdjustCapacity() {
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		s.log.WithError(err).Error("Failed to create AWS session")
		return
	}

	svc := ecs.New(session)
	tasks, err := GetCapacityProviderTasks(svc, s.CapacityProviderName)
	if err != nil {
		s.log.WithError(err).Error("Failed to get list of running task")
		return
	}

	autoscalingSvc := autoscaling.New(session)
	asg, err := s.getAutoscalingGroup(autoscalingSvc)
	if err != nil {
		s.log.WithError(err).Error("Failed to get autoscaling group")
		return
	}

	currentDesiredCapacity := *asg.DesiredCapacity
	freeResources, requiredResources := s.calculateResources(tasks, int(currentDesiredCapacity), "RUNNING", "PROVISIONING")

	if resourcesToAllocate := s.getResourcesForAllocation(); resourcesToAllocate != nil {
		for _, resources := range resourcesToAllocate {
			enough := false
			for _, i := range freeResources {
				if i.CPU >= resources.Cpu && i.Memory >= resources.Memory {
					i.CPU -= resources.Cpu
					i.Memory -= resources.Memory
					enough = true
					break
				}
			}

			if !enough {
				requiredResources = append(requiredResources, &Resources{CPU: resources.Cpu, Memory: resources.Memory})
			}
		}
	}

	if len(requiredResources) == 0 {
		s.ScaleDown(session, asg, freeResources)
	} else {
		s.ScaleUp(session, asg, requiredResources)
	}
}

func (s *scaler) ScaleUp(session *awsSession.Session, asg *autoscaling.Group, requiredResources []*Resources) {
	// No new resources required right now
	if len(requiredResources) == 0 {
		s.log.Trace("No new resources required")
		return
	}

	totalRequiredResources := Resources{
		CPU:    0,
		Memory: 0,
	}
	for _, t := range requiredResources {
		totalRequiredResources.CPU += t.CPU
		totalRequiredResources.Memory += t.Memory
	}
	s.log.WithFields(log.Fields{
		"CPU":    totalRequiredResources.CPU,
		"Memory": totalRequiredResources.Memory,
	}).Debug("Total required resources")

	requiredCpu := float64(totalRequiredResources.CPU) / float64(s.resourcesPerWeight.CPU)
	requiredMemory := float64(totalRequiredResources.Memory) / float64(s.resourcesPerWeight.Memory)

	currentCapacity := *asg.DesiredCapacity
	desiredCapacity := float64(currentCapacity) + math.Max(requiredCpu, requiredMemory)
	desiredReservationCapacity := desiredCapacity * (1 + config.Conf.ReserveInstancesPercent)

	if desiredReservationCapacity-desiredCapacity > float64(config.Conf.ReserveMaxCapacity) {
		s.log.WithFields(log.Fields{
			"desired reservation capacity": math.Ceil(desiredReservationCapacity),
			"desired capacity":             math.Ceil(desiredCapacity),
			"max reservation capacity":     config.Conf.ReserveMaxCapacity,
		}).Warn("Triggered max reservation capacity limit")
		desiredReservationCapacity = desiredCapacity + float64(config.Conf.ReserveMaxCapacity)
	}

	newCapacity := int64(math.Ceil(desiredReservationCapacity))

	if newCapacity > *asg.MaxSize {
		s.log.WithFields(log.Fields{
			"maxCapacity":     *asg.MaxSize,
			"desiredCapacity": newCapacity,
		}).Warn("ASG desired size reached limit!")
		newCapacity = *asg.MaxSize
	}

	if newCapacity == *asg.DesiredCapacity {
		// do nothing
		return
	}

	autoscalingSvc := autoscaling.New(session)
	err := s.SetDesiredCapacity(*autoscalingSvc, newCapacity)
	if err != nil {
		s.log.WithError(err).Error("Failed to update auto scaling group")
		return
	}
	s.log.WithFields(log.Fields{
		"oldCapacity": currentCapacity,
		"newCapacity": newCapacity,
	}).Info("Capacity updated")
}

func (s *scaler) ScaleDown(session *awsSession.Session, asg *autoscaling.Group, freeResources []*Resources) {
	autoscalingSvc := autoscaling.New(session)
	instancesDetailsMap, err := s.getInstancesInAutoscalingGroup(autoscalingSvc)
	if err != nil {
		s.log.WithError(err).Info("Failed to get instances in autoscaling group")
		return
	}

	minSize, desiredCapacity := *asg.MinSize, *asg.DesiredCapacity

	var currentCapacity int64 = 0
	for _, instance := range instancesDetailsMap {
		weight, _ := strconv.ParseInt(*instance.WeightedCapacity, 10, 64)
		currentCapacity += weight
	}

	overLimitCapacity := currentCapacity - desiredCapacity
	if overLimitCapacity > 0 {
		freeResources = append(freeResources, s.generateEmptyResources(int(overLimitCapacity))...)
	}

	allowedCapacityForDeleting := 0
	for _, instance := range freeResources {
		if s.exporterResources == nil {
			if instance.CPU >= s.resourcesPerWeight.CPU && instance.Memory >= s.resourcesPerWeight.Memory {
				allowedCapacityForDeleting++
			}
		} else {
			if instance.CPU+s.exporterResources.CPU >= s.resourcesPerWeight.CPU &&
				instance.Memory+s.exporterResources.Memory >= s.resourcesPerWeight.Memory {
				allowedCapacityForDeleting++
			}
		}
	}

	if allowedCapacityForDeleting == 0 {
		s.log.Trace("All instances are busy, scale down not allowed")
		return
	}

	svc := ecs.New(session)
	ciArns, err := ListContainerInstances(svc)
	if err != nil {
		s.log.WithError(err).Debug("Failed to list container instances")
		return
	}

	containerInstances, err := DescribeActiveContainerInstancesOfCapacityProvider(ciArns, svc, s.CapacityProviderName)
	if err != nil {
		s.log.WithError(err).Error("Failed to describe container instances")
		return
	}

	// [instance]weight map
	allowedInstancesToDelete := make(map[*ecs.ContainerInstance]int64)
	capacityToDelete := 0
	for _, instance := range containerInstances {
		if allowedCapacityForDeleting <= 0 {
			break
		}

		totalTasks := *instance.PendingTasksCount + *instance.RunningTasksCount
		if (s.exporterResources == nil && totalTasks == 0) ||
			(s.exporterResources != nil && totalTasks <= 1) {
			instanceUptime := time.Since(*instance.RegisteredAt)
			if instanceUptime > config.Conf.InstanceCooldownTimeout && instance.Ec2InstanceId != nil {
				instanceDetails, ok := instancesDetailsMap[*instance.Ec2InstanceId]
				if !ok || instanceDetails.WeightedCapacity == nil {
					continue
				}

				weight, _ := strconv.ParseInt(*instanceDetails.WeightedCapacity, 10, 64)
				capacityToDelete += int(weight)
				allowedInstancesToDelete[instance] = weight
				allowedCapacityForDeleting -= int(weight)
			}
		}
	}

	// ReserveInstancesPercent on scalde down makes less instance termination that it could be done (scales down slower)
	capacityToDeleteReserved := float64(capacityToDelete) * (1 - config.Conf.ReserveInstancesPercent)
	if float64(capacityToDelete)-capacityToDeleteReserved > float64(config.Conf.ReserveMaxCapacity) {
		maxCapacityToDelete := float64(int64(capacityToDelete) - config.Conf.ReserveMaxCapacity)
		s.log.WithFields(log.Fields{
			"wantedCapacityToDelete":   capacityToDelete,
			"capacityToDeleteReserved": math.Ceil(capacityToDeleteReserved),
			"maxReservationCapacity":   config.Conf.ReserveMaxCapacity,
			"maxCapacityToDelete":      maxCapacityToDelete,
		}).Warn("Triggered max reservation capacity limit")
		capacityToDeleteReserved = maxCapacityToDelete
	}

	maxCapacityToDelete := int64(math.Ceil(capacityToDeleteReserved))

	instancesToDelete := make([]*string, 0)
	containerInstanceToDelete := make([]*string, 0)
	newCapacity := currentCapacity
	for instance, weight := range allowedInstancesToDelete {
		if newCapacity <= minSize || maxCapacityToDelete <= 0 {
			break
		}

		if newCapacity-weight < minSize {
			if minSize != 0 {
				continue
			}
			weight = newCapacity
		}

		s.log.WithField("instance", *instance.Ec2InstanceId).Trace("Stopping instance")
		instancesToDelete = append(instancesToDelete, instance.Ec2InstanceId)
		containerInstanceToDelete = append(containerInstanceToDelete, instance.ContainerInstanceArn)

		newCapacity -= weight
		maxCapacityToDelete -= weight
	}

	if len(instancesToDelete) == 0 {
		return
	}

	ciPages := utils.Paginate(containerInstanceToDelete, 10)
	for _, ciArr := range ciPages {
		stateUpdateInput := ecs.UpdateContainerInstancesStateInput{
			Cluster:            &config.Conf.AwsCluster,
			Status:             aws.String("DRAINING"),
			ContainerInstances: ciArr,
		}

		ecsSvc := ecs.New(session)
		_, err = utils.RetryThrottling(ecsSvc.UpdateContainerInstancesState)(&stateUpdateInput)
		if err != nil {
			s.log.WithError(err).Info("Failed to change status of stopping contaienr-instances to draining")
			return
		}
	}

	instancesPages := utils.Paginate(instancesToDelete, 50)
	for _, instances := range instancesPages {
		scaleInDisableInput := autoscaling.SetInstanceProtectionInput{
			AutoScalingGroupName: &s.autoscalingGroupName,
			InstanceIds:          instances,
			ProtectedFromScaleIn: aws.Bool(false),
		}

		_, err = utils.RetryThrottling(autoscalingSvc.SetInstanceProtection)(&scaleInDisableInput)
		if err != nil {
			s.log.WithError(err).Info("Failed to diable scale-in protection")
			return
		}
	}

	if newCapacity >= desiredCapacity {
		s.log.WithFields(log.Fields{"desiredCapacity": desiredCapacity, "terminatedCapacity": currentCapacity - newCapacity}).Info("Instance rebalance were performed without desired capacity change")
		return
	}

	err = s.SetDesiredCapacity(*autoscalingSvc, newCapacity)
	if err != nil {
		s.log.WithError(err).Error("Failed to update auto scaling group")
		return
	}

	s.log.WithFields(log.Fields{
		"oldCapacity": desiredCapacity,
		"newCapacity": newCapacity,
	}).Info("Capacity updated")
}

func (s *scaler) StopEc2ZombieInstances() {
	l := log.WithField("asg", s.autoscalingGroupName)
	session, err := awsSession.NewSession(&aws.Config{Region: &config.Conf.AwsRegion, MaxRetries: &config.Conf.AwsRetry})
	if err != nil {
		l.WithError(err).Error("Failed to create AWS session")
		return
	}

	svc := ecs.New(session)
	ciArns, err := ListContainerInstances(svc)
	if err != nil {
		l.WithError(err).Debug("Failed to list container instances")
		return
	}

	containerInstances, err := DescribeActiveContainerInstancesOfCapacityProvider(ciArns, svc, s.CapacityProviderName)
	if err != nil {
		l.WithError(err).Error("Failed to describe container instances")
		return
	}

	ec2Svc := ec2.New(session)
	instances, err := DescribeInstancesByAsgName(&s.autoscalingGroupName, ec2Svc)
	if err != nil {
		l.WithError(err).Error("Failed to describe ec2 instances")
		return
	}

	instancesToStop := []*ec2.Instance{}
	for i := 0; i < len(instances); i++ {
		instance := instances[i]
		found := false
		for j := 0; j < len(containerInstances); j++ {
			if *containerInstances[j].Ec2InstanceId == *instance.InstanceId {
				containerInstances = deleteElement(containerInstances, j)
				found = true
				break
			}
		}

		if !found &&
			instance.State != nil && *instance.State.Code == 16 &&
			instance.LaunchTime != nil && time.Since(*instance.LaunchTime) > config.Conf.ContainerInstanceInitTimeout {
			instancesToStop = append(instancesToStop, instances[i])
		}
	}

	autoscalingSvc := autoscaling.New(session)
	for _, instance := range instancesToStop {
		l.WithField("instance-id", *instance.InstanceId).WithField("initTimeout", config.Conf.ContainerInstanceInitTimeout.Minutes()).Error("Stopping instance as it failed to init container-instance")
		stopInstanceInput := autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instance.InstanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(false),
		}
		_, err := utils.RetryThrottling(autoscalingSvc.TerminateInstanceInAutoScalingGroup)(&stopInstanceInput)
		if err != nil {
			l.WithError(err).Error("Failed to stop instance")
		}

		time.Sleep(250 * time.Millisecond)
	}
}

func (s *scaler) SetDesiredCapacity(autoscalingSvc autoscaling.AutoScaling, newCapacity int64) error {
	setDesiredCapacityInput := autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: &s.autoscalingGroupName,
		DesiredCapacity:      &newCapacity,
	}
	_, err := utils.RetryThrottling(autoscalingSvc.SetDesiredCapacity)(&setDesiredCapacityInput)

	return err
}

func deleteElement(slice []*ecs.ContainerInstance, index int) []*ecs.ContainerInstance {
	return append(slice[:index], slice[index+1:]...)
}
