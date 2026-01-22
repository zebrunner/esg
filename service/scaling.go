package service

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	autoscalingTypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2Types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/resourcesToAllocate"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/utils"
)

type scaler struct {
	InstanceResources    Resources
	CapacityProviderName string
	autoscalingGroupName string
	resourcesPerWeight   Resources
	instanceMinWeight    int64
	log                  *log.Entry
}

type Resources struct {
	CPU    int64
	Memory int64
}

func StartScalers(ctx context.Context, scalersMap map[string]scaler) {
	for _, s := range scalersMap {
		go func(s scaler) {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(15 * time.Second):
					s.AdjustCapacity(ctx)
				}
			}
		}(s)

		go func(s scaler) {
			for {
				select {
				case <-ctx.Done():
					return
				case <-time.After(10 * time.Minute):
					s.StopEc2ZombieInstances(ctx)
				}
			}
		}(s)

		log.WithFields(log.Fields{"instanceResources": s.InstanceResources, "minInstanceWeight": s.instanceMinWeight, "capacityProvider": s.CapacityProviderName, "asg": s.autoscalingGroupName}).Info("Started scaler")
	}
}

func InitScalingData(ctx context.Context) (map[string]scaler, error) {
	ecsSvc := ecs.NewFromConfig(AwsCfg)

	describeClusterInput := &ecs.DescribeClustersInput{
		Clusters: []string{config.Conf.AwsCluster},
	}
	describeClusterOutput, err := ecsSvc.DescribeClusters(ctx, describeClusterInput)
	if err != nil {
		return nil, err
	} else if len(describeClusterOutput.Clusters) == 0 {
		return nil, fmt.Errorf("failed to describe cluster: %s", config.Conf.AwsCluster)
	}

	describeCapacityProvidersInput := &ecs.DescribeCapacityProvidersInput{
		CapacityProviders: describeClusterOutput.Clusters[0].CapacityProviders,
	}
	describeCapacityProvidersOutput, err := ecsSvc.DescribeCapacityProviders(ctx, describeCapacityProvidersInput)
	if err != nil {
		return nil, err
	} else if len(describeCapacityProvidersOutput.CapacityProviders) == 0 {
		return nil, fmt.Errorf("failed to describe capacity providers")
	}

	scalers := make(map[string]scaler)
	for _, capacityProvider := range describeCapacityProvidersOutput.CapacityProviders {
		name := aws.ToString(capacityProvider.Name)

		if isFargate(capacityProvider) {
			log.Printf("Skipping capacity provider %q (Fargate).", name)
			continue
		}

		if capacityProvider.AutoScalingGroupProvider == nil ||
			capacityProvider.AutoScalingGroupProvider.AutoScalingGroupArn == nil {
			log.Printf("Skipping capacity provider %q: no AutoScalingGroupProvider.", name)
			continue
		}

		asgArn := capacityProvider.AutoScalingGroupProvider.AutoScalingGroupArn
		asgArnSplited := strings.Split(*asgArn, "/")
		asgName := asgArnSplited[len(asgArnSplited)-1]

		autoscalingSvc := autoscaling.NewFromConfig(AwsCfg)
		describeGroupInput := &autoscaling.DescribeAutoScalingGroupsInput{
			AutoScalingGroupNames: []string{asgName},
		}
		describeGroupOutput, err := autoscalingSvc.DescribeAutoScalingGroups(ctx, describeGroupInput)
		if err != nil {
			return nil, err
		}

		instanceOverrides, err := getOverrides(describeGroupOutput)
		if err != nil {
			return nil, err
		}

		// From doc: Value must be in the range of 1 to 999.
		instanceWithMinWeight := &autoscalingTypes.LaunchTemplateOverrides{
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
				instanceWithMinWeight = &instanceOverrides[i]
			}
		}

		ec2Svc := ec2.NewFromConfig(AwsCfg)
		describeInstanceTypeInput := &ec2.DescribeInstanceTypesInput{
			InstanceTypes: []ec2Types.InstanceType{
				ec2Types.InstanceType(aws.ToString(instanceWithMinWeight.InstanceType)),
			},
		}
		instanceTypesResult, err := ec2Svc.DescribeInstanceTypes(ctx, describeInstanceTypeInput)
		if err != nil {
			return nil, err
		}

		instanceInfo := instanceTypesResult.InstanceTypes[0]

		s := scaler{
			CapacityProviderName: aws.ToString(capacityProvider.Name),
			autoscalingGroupName: asgName,
			resourcesPerWeight:   Resources{CPU: (int64(aws.ToInt32(instanceInfo.VCpuInfo.DefaultVCpus)) * 1024) / minWeight, Memory: aws.ToInt64(instanceInfo.MemoryInfo.SizeInMiB) / minWeight},
			InstanceResources:    Resources{CPU: int64(aws.ToInt32(instanceInfo.VCpuInfo.DefaultVCpus)) * 1024, Memory: aws.ToInt64(instanceInfo.MemoryInfo.SizeInMiB)},
			instanceMinWeight:    minWeight,
			log:                  log.WithFields(log.Fields{"asg": asgName, "capacityProvider": aws.ToString(capacityProvider.Name)}),
		}

		scalers[s.CapacityProviderName] = s
	}

	return scalers, nil
}

func getTasksResources(tasks []ecsTypes.Task, status string) []*Resources {
	resources := []*Resources{}
	for _, task := range tasks {
		taskCpu, cpuErr := strconv.ParseInt(aws.ToString(task.Cpu), 10, 64)
		taskMemory, memoryErr := strconv.ParseInt(aws.ToString(task.Memory), 10, 64)
		if aws.ToString(task.LastStatus) == status && cpuErr == nil && memoryErr == nil {
			resources = append(resources, &Resources{
				CPU:    taskCpu,
				Memory: taskMemory,
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

func (s *scaler) calculateResources(tasks []ecsTypes.Task, currentCapacity int, statuses ...string) (freeResources []*Resources, requiredResources []*Resources) {
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

func (s *scaler) getInstancesInAutoscalingGroup(ctx context.Context, autoscalingSvc *autoscaling.Client) (map[string]autoscalingTypes.AutoScalingInstanceDetails, error) {
	instancesMap := make(map[string]autoscalingTypes.AutoScalingInstanceDetails)
	instanceIdsArr := make([]string, 0)

	paginator := autoscaling.NewDescribeAutoScalingInstancesPaginator(autoscalingSvc, &autoscaling.DescribeAutoScalingInstancesInput{})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, err
		}

		for _, instance := range page.AutoScalingInstances {
			if instance.AutoScalingGroupName == nil || *instance.AutoScalingGroupName != s.autoscalingGroupName {
				continue
			}

			instancesMap[aws.ToString(instance.InstanceId)] = instance
			instanceIdsArr = append(instanceIdsArr, aws.ToString(instance.InstanceId))
		}
	}

	if len(instanceIdsArr) == 0 {
		return instancesMap, nil
	}

	ec2Svc := ec2.NewFromConfig(AwsCfg)
	ec2Instances, err := DescribeInstances(ctx, instanceIdsArr, ec2Svc)
	if err != nil {
		return nil, err
	}

	// filter by ec2 instance state and scale-in protection
	for _, ec2Instance := range ec2Instances {
		if ec2Instance.State == nil || ec2Instance.State.Code == nil {
			delete(instancesMap, aws.ToString(ec2Instance.InstanceId))
			continue
		}

		// 0 - pending
		if *ec2Instance.State.Code == 0 {
			continue
		}

		// 16 - running
		if *ec2Instance.State.Code == 16 {
			isProtected := false
			var details autoscalingTypes.AutoScalingInstanceDetails
			if details, isProtected = instancesMap[aws.ToString(ec2Instance.InstanceId)]; isProtected {
				isProtected = aws.ToBool(details.ProtectedFromScaleIn)
			}

			if !isProtected {
				delete(instancesMap, aws.ToString(ec2Instance.InstanceId))
			}

			continue
		}

		delete(instancesMap, aws.ToString(ec2Instance.InstanceId))
	}

	return instancesMap, nil
}

func (s *scaler) getAutoscalingGroup(ctx context.Context, autoscalingSvc *autoscaling.Client) (*autoscalingTypes.AutoScalingGroup, error) {
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{s.autoscalingGroupName},
	}
	describeAutoScalingGroupsOutput, err := autoscalingSvc.DescribeAutoScalingGroups(ctx, describeAutoScalingGroupsInput)
	if err != nil {
		log.WithError(err).Error("Failed to describe auto scaling group.")
		return nil, err
	}

	if len(describeAutoScalingGroupsOutput.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("autoscaling group with name %s not found", s.autoscalingGroupName)
	}
	autoScalingGroup := &describeAutoScalingGroupsOutput.AutoScalingGroups[0]

	return autoScalingGroup, nil
}

func (s *scaler) AdjustCapacity(ctx context.Context) {
	svc := ecs.NewFromConfig(AwsCfg)
	tasks, err := GetCapacityProviderTasks(ctx, svc, s.CapacityProviderName)
	if err != nil {
		s.log.WithError(err).Error("Failed to get list of running task")
		return
	}

	autoscalingSvc := autoscaling.NewFromConfig(AwsCfg)
	asg, err := s.getAutoscalingGroup(ctx, autoscalingSvc)
	if err != nil {
		s.log.WithError(err).Error("Failed to get autoscaling group")
		return
	}

	currentDesiredCapacity := aws.ToInt32(asg.DesiredCapacity)
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
		s.ScaleDown(ctx, asg, freeResources)
	} else {
		s.ScaleUp(ctx, asg, requiredResources)
	}
}

func (s *scaler) ScaleUp(ctx context.Context, asg *autoscalingTypes.AutoScalingGroup, requiredResources []*Resources) {
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

	currentCapacity := aws.ToInt32(asg.DesiredCapacity)
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

	if newCapacity > int64(aws.ToInt32(asg.MaxSize)) {
		s.log.WithFields(log.Fields{
			"maxCapacity":     aws.ToInt32(asg.MaxSize),
			"desiredCapacity": newCapacity,
		}).Warn("ASG desired size reached limit!")
		newCapacity = int64(aws.ToInt32(asg.MaxSize))
	}

	if newCapacity == int64(aws.ToInt32(asg.DesiredCapacity)) {
		// do nothing
		return
	}

	autoscalingSvc := autoscaling.NewFromConfig(AwsCfg)
	err := s.SetDesiredCapacity(ctx, autoscalingSvc, newCapacity)
	if err != nil {
		s.log.WithError(err).Error("Failed to update auto scaling group")
		return
	}
	s.log.WithFields(log.Fields{
		"oldCapacity": currentCapacity,
		"newCapacity": newCapacity,
	}).Info("Capacity updated")
}

func (s *scaler) ScaleDown(ctx context.Context, asg *autoscalingTypes.AutoScalingGroup, freeResources []*Resources) {
	autoscalingSvc := autoscaling.NewFromConfig(AwsCfg)
	instancesDetailsMap, err := s.getInstancesInAutoscalingGroup(ctx, autoscalingSvc)
	if err != nil {
		s.log.WithError(err).Info("Failed to get instances in autoscaling group")
		return
	}

	minSize, desiredCapacity := int64(aws.ToInt32(asg.MinSize)), int64(aws.ToInt32(asg.DesiredCapacity))

	var currentCapacity int64 = 0
	for _, instance := range instancesDetailsMap {
		weight, _ := strconv.ParseInt(aws.ToString(instance.WeightedCapacity), 10, 64)
		currentCapacity += weight
	}

	overLimitCapacity := currentCapacity - desiredCapacity
	if overLimitCapacity > 0 {
		freeResources = append(freeResources, s.generateEmptyResources(int(overLimitCapacity))...)
	}

	allowedCapacityForDeleting := 0
	for _, instance := range freeResources {
		if instance.CPU >= s.resourcesPerWeight.CPU && instance.Memory >= s.resourcesPerWeight.Memory {
			allowedCapacityForDeleting++
		}
	}

	if allowedCapacityForDeleting == 0 {
		s.log.Trace("All instances are busy, scale down not allowed")
		return
	}

	svc := ecs.NewFromConfig(AwsCfg)
	ciArns, err := ListContainerInstances(ctx, svc)
	if err != nil {
		s.log.WithError(err).Debug("Failed to list container instances")
		return
	}

	containerInstances, err := DescribeActiveContainerInstancesOfCapacityProvider(ctx, ciArns, svc, s.CapacityProviderName)
	if err != nil {
		s.log.WithError(err).Error("Failed to describe container instances")
		return
	}

	// [instance]weight map
	allowedInstancesToDelete := make(map[*ecsTypes.ContainerInstance]int64)
	capacityToDelete := int64(0)
	for i := range containerInstances {
		instance := &containerInstances[i]
		if allowedCapacityForDeleting <= 0 {
			break
		}

		if instance.PendingTasksCount == 0 && instance.RunningTasksCount == 0 {
			instanceUptime := time.Since(aws.ToTime(instance.RegisteredAt))
			if instanceUptime > config.Conf.InstanceCooldownTimeout && instance.Ec2InstanceId != nil {
				instanceDetails, ok := instancesDetailsMap[*instance.Ec2InstanceId]
				if !ok || instanceDetails.WeightedCapacity == nil {
					continue
				}

				weight, _ := strconv.ParseInt(aws.ToString(instanceDetails.WeightedCapacity), 10, 64)
				capacityToDelete += weight
				allowedInstancesToDelete[instance] = weight
				allowedCapacityForDeleting -= int(weight)
			}
		}
	}

	// ReserveInstancesPercent on scalde down makes less instance termination that it could be done (scales down slower)
	capacityToDeleteReserved := float64(capacityToDelete) * (1 - config.Conf.ReserveInstancesPercent)
	if float64(capacityToDelete)-capacityToDeleteReserved > float64(config.Conf.ReserveMaxCapacity) {
		maxCapacityToDelete := float64(capacityToDelete) - float64(config.Conf.ReserveMaxCapacity)
		s.log.WithFields(log.Fields{
			"wantedCapacityToDelete":   capacityToDelete,
			"capacityToDeleteReserved": math.Ceil(capacityToDeleteReserved),
			"maxReservationCapacity":   config.Conf.ReserveMaxCapacity,
			"maxCapacityToDelete":      maxCapacityToDelete,
		}).Warn("Triggered max reservation capacity limit")
		capacityToDeleteReserved = maxCapacityToDelete
	}

	maxCapacityToDelete := int64(math.Ceil(capacityToDeleteReserved))

	instancesToDelete := make([]string, 0)
	containerInstanceToDelete := make([]string, 0)
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

		s.log.WithField("instance", aws.ToString(instance.Ec2InstanceId)).Trace("Stopping instance")
		instancesToDelete = append(instancesToDelete, aws.ToString(instance.Ec2InstanceId))
		containerInstanceToDelete = append(containerInstanceToDelete, aws.ToString(instance.ContainerInstanceArn))

		newCapacity -= weight
		maxCapacityToDelete -= weight
	}

	if len(instancesToDelete) == 0 {
		return
	}

	ciPages := utils.Paginate(containerInstanceToDelete, 10)
	for _, ciArr := range ciPages {
		stateUpdateInput := &ecs.UpdateContainerInstancesStateInput{
			Cluster:            aws.String(config.Conf.AwsCluster),
			Status:             ecsTypes.ContainerInstanceStatusDraining,
			ContainerInstances: ciArr,
		}

		ecsSvc := ecs.NewFromConfig(AwsCfg)
		_, err = ecsSvc.UpdateContainerInstancesState(ctx, stateUpdateInput)
		if err != nil {
			s.log.WithError(err).Info("Failed to change status of stopping contaienr-instances to draining")
			return
		}
	}

	instancesPages := utils.Paginate(instancesToDelete, 50)
	for _, instances := range instancesPages {
		scaleInDisableInput := &autoscaling.SetInstanceProtectionInput{
			AutoScalingGroupName: aws.String(s.autoscalingGroupName),
			InstanceIds:          instances,
			ProtectedFromScaleIn: aws.Bool(false),
		}

		_, err = autoscalingSvc.SetInstanceProtection(ctx, scaleInDisableInput)
		if err != nil {
			s.log.WithError(err).Info("Failed to diable scale-in protection")
			return
		}
	}

	if newCapacity >= desiredCapacity {
		s.log.WithFields(log.Fields{"desiredCapacity": desiredCapacity, "terminatedCapacity": currentCapacity - newCapacity}).Info("Instance rebalance were performed without desired capacity change")
		return
	}

	err = s.SetDesiredCapacity(ctx, autoscalingSvc, newCapacity)
	if err != nil {
		s.log.WithError(err).Error("Failed to update auto scaling group")
		return
	}

	s.log.WithFields(log.Fields{
		"oldCapacity": desiredCapacity,
		"newCapacity": newCapacity,
	}).Info("Capacity updated")
}

func (s *scaler) StopEc2ZombieInstances(ctx context.Context) {
	l := log.WithField("asg", s.autoscalingGroupName)

	svc := ecs.NewFromConfig(AwsCfg)
	ciArns, err := ListContainerInstances(ctx, svc)
	if err != nil {
		l.WithError(err).Debug("Failed to list container instances")
		return
	}

	containerInstances, err := DescribeActiveContainerInstancesOfCapacityProvider(ctx, ciArns, svc, s.CapacityProviderName)
	if err != nil {
		l.WithError(err).Error("Failed to describe container instances")
		return
	}

	ec2Svc := ec2.NewFromConfig(AwsCfg)
	instances, err := DescribeInstancesByAsgName(ctx, s.autoscalingGroupName, ec2Svc)
	if err != nil {
		l.WithError(err).Error("Failed to describe ec2 instances")
		return
	}

	instancesToStop := []ec2Types.Instance{}
	for i := 0; i < len(instances); i++ {
		instance := instances[i]
		found := false
		for j := 0; j < len(containerInstances); j++ {
			if aws.ToString(containerInstances[j].Ec2InstanceId) == aws.ToString(instance.InstanceId) {
				containerInstances = deleteElement(containerInstances, j)
				found = true
				break
			}
		}

		if !found &&
			instance.State != nil && aws.ToInt32(instance.State.Code) == 16 &&
			instance.LaunchTime != nil && time.Since(aws.ToTime(instance.LaunchTime)) > config.Conf.ContainerInstanceInitTimeout {
			instancesToStop = append(instancesToStop, instances[i])
		}
	}

	autoscalingSvc := autoscaling.NewFromConfig(AwsCfg)
	for _, instance := range instancesToStop {
		l.WithField("instance-id", aws.ToString(instance.InstanceId)).WithField("initTimeout", config.Conf.ContainerInstanceInitTimeout.Minutes()).Error("Stopping instance as it failed to init container-instance")
		stopInstanceInput := &autoscaling.TerminateInstanceInAutoScalingGroupInput{
			InstanceId:                     instance.InstanceId,
			ShouldDecrementDesiredCapacity: aws.Bool(false),
		}
		_, err := autoscalingSvc.TerminateInstanceInAutoScalingGroup(ctx, stopInstanceInput)
		if err != nil {
			l.WithError(err).Error("Failed to stop instance")
		}

		time.Sleep(250 * time.Millisecond)
	}
}

func (s *scaler) SetDesiredCapacity(ctx context.Context, autoscalingSvc *autoscaling.Client, newCapacity int64) error {
	setDesiredCapacityInput := &autoscaling.SetDesiredCapacityInput{
		AutoScalingGroupName: aws.String(s.autoscalingGroupName),
		DesiredCapacity:      aws.Int32(int32(newCapacity)),
	}
	_, err := autoscalingSvc.SetDesiredCapacity(ctx, setDesiredCapacityInput)

	return err
}

func deleteElement(slice []ecsTypes.ContainerInstance, index int) []ecsTypes.ContainerInstance {
	return append(slice[:index], slice[index+1:]...)
}

func getOverrides(out *autoscaling.DescribeAutoScalingGroupsOutput) ([]autoscalingTypes.LaunchTemplateOverrides, error) {
	if out == nil || len(out.AutoScalingGroups) == 0 {
		return nil, fmt.Errorf("no AutoScalingGroups in response")
	}
	g := out.AutoScalingGroups[0]
	if g.MixedInstancesPolicy == nil {
		return nil, fmt.Errorf("ASG does not use MixedInstancesPolicy")
	}
	if g.MixedInstancesPolicy.LaunchTemplate == nil {
		return nil, fmt.Errorf("MixedInstancesPolicy has no LaunchTemplate")
	}
	if g.MixedInstancesPolicy.LaunchTemplate.Overrides == nil {
		return nil, fmt.Errorf("no Overrides on MixedInstancesPolicy.LaunchTemplate")
	}
	return g.MixedInstancesPolicy.LaunchTemplate.Overrides, nil
}

func isFargate(cp ecsTypes.CapacityProvider) bool {
	if cp.Name == nil {
		return false
	}
	n := strings.ToUpper(aws.ToString(cp.Name))
	return n == "FARGATE" || n == "FARGATE_SPOT"
}
