package service

import (
	"errors"
	"fmt"
	"log"
	"math"
	"strconv"

	"github.com/aws/aws-sdk-go/aws"
	awsSession "github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/aws/aws-sdk-go/service/ecs"
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
		Cluster: &AwsCluster,
	}
	listInstancesOutput, err := svc.ListContainerInstances(listInstancesInput)
	if err != nil {
		return 0, err
	}
	instanceCount := len(listInstancesOutput.ContainerInstanceArns)
	return int64(instanceCount), nil
}

func getTasksResources(svc *ecs.ECS, taskStatus string) (*Resources, error) {
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &AwsCluster,
	}
	listTasksResult, err := svc.ListTasks(listTasksInput)
	if err != nil {
		return nil, err
	}
	if len(listTasksResult.TaskArns) == 0 {
		return nil, errors.New("can't describe tasks")
	}
	describeTasksInput := &ecs.DescribeTasksInput{
		Cluster: &AwsCluster,
		Tasks:   listTasksResult.TaskArns,
	}
	describeTasksResult, err := svc.DescribeTasks(describeTasksInput)
	if err != nil {
		return nil, err
	}
	// Calculate provisioning tasks resources
	var CPU int64 = 0
	var Memory int64 = 0
	for _, task := range describeTasksResult.Tasks {
		if *task.LastStatus == taskStatus {
			taskCPU, err := strconv.ParseInt(*task.Cpu, 10, 64)
			if err != nil {
				return nil, err
			}
			taskMemory, err := strconv.ParseInt(*task.Memory, 10, 64)
			if err != nil {
				return nil, err
			}
			CPU += taskCPU
			Memory += taskMemory
		}
	}

	return &Resources{
		CPU:    CPU,
		Memory: Memory,
	}, nil
}

func setDesiredCapacity(autoscalingService *autoscaling.AutoScaling, newDesiredCapacity int64) {
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{&AwsAutoScalingGroup},
	}
	describeAutoScalingGroupsOutput, err := autoscalingService.DescribeAutoScalingGroups(describeAutoScalingGroupsInput)
	if err != nil {
		log.Println("cant describe autoscaling group", err)
		return
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]
	if newDesiredCapacity < *autoScalingGroup.DesiredCapacity {
		log.Printf("[WARN] Scaled down not allowed")
		return
	}

	if newDesiredCapacity > *autoScalingGroup.MaxSize {
		log.Printf("[WARN] [LIMIT REACHED] ASG desired size reached limit! MaxCount: %d DesiredCount: %d!", *autoScalingGroup.MaxSize, newDesiredCapacity)
		newDesiredCapacity = *autoScalingGroup.MaxSize
	}
	updateGroupInput := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: autoScalingGroup.AutoScalingGroupName,
		DesiredCapacity:      &newDesiredCapacity,
	}
	_, err = autoscalingService.UpdateAutoScalingGroup(updateGroupInput)
	if err != nil {
		log.Println("Error while updating group", err)
	} else {
		log.Printf("Capacity updated from %d to %d \n", *autoScalingGroup.DesiredCapacity, newDesiredCapacity)
	}
}

func ScaleUp() {
	session, err := awsSession.NewSession(&aws.Config{Region: &AwsRegion, MaxRetries: &AwsRetry})
	if err != nil {
		log.Fatal(err)
		return
	}
	svc := ecs.New(session)
	autoscalingSvc := autoscaling.New(session)

	runningTasksResources, err := getTasksResources(svc, "RUNNING")
	if err != nil {
		log.Println("Error while getting running tasks resources.", err)
		return
	}

	provisioningTasksResources, err := getTasksResources(svc, "PROVISIONING")
	if err != nil {
		log.Println("Error while getting provisioning tasks resources.", err)
		return
	}

	// All tasks is running
	if provisioningTasksResources.CPU == 0 && provisioningTasksResources.Memory == 0 {
		log.Println("There is no tasks in PROVISIONING state. No need to scale up")
		return
	}

	allTasksCPU := runningTasksResources.CPU + provisioningTasksResources.CPU
	allTasksMemory := runningTasksResources.Memory + provisioningTasksResources.Memory
	cpuRatio := float64(allTasksCPU) / float64(runningTasksResources.CPU)
	fmt.Println("RunningTasksCpu:", runningTasksResources.CPU, "ProvisioningTaskCpu", provisioningTasksResources.CPU, "Ratio:", cpuRatio)
	memoryRatio := float64(allTasksMemory) / float64(runningTasksResources.Memory)
	fmt.Println("RunningTasksMemory:", runningTasksResources.Memory, "ProvisioningTaskMemory", provisioningTasksResources.Memory, "Ratio:", memoryRatio)
	scaleRatio := math.Max(cpuRatio, memoryRatio)
	fmt.Println("ScaleRatio:", scaleRatio)

	curentInstanceCount, err := getInstanceCount(svc)
	if err != nil {
		log.Println("Error while getting instance count", err)
	}

	scaledDesiredCount := int64(math.Ceil(float64(curentInstanceCount) * scaleRatio))
	fmt.Println("ScaledDesiredCount:", scaledDesiredCount, "CurrentInstacneCount", curentInstanceCount)
	setDesiredCapacity(autoscalingSvc, scaledDesiredCount)
}
