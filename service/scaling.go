package service

import (
	"errors"
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
	CurrentResources       Resources
	ReservedResources     Resources
	ProvisioningResources Resources
}

func getInstanceCount(svc *ecs.ECS) (int64 , error) {
	listInstancesInput := &ecs.ListContainerInstancesInput{
		Cluster: aws.String("executor-cluster"),
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
		Cluster: aws.String("executor-cluster"),
	}
	listTasksResult, err := svc.ListTasks(listTasksInput)
	if err != nil {
		return nil, err
	}
	if len(listTasksResult.TaskArns) == 0 {
		return nil, errors.New("can't describe tasks")
	}
	describeTasksInput := &ecs.DescribeTasksInput{
		Cluster: aws.String("executor-cluster"),
		Tasks: listTasksResult.TaskArns,
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
		CPU: CPU,
		Memory: Memory,
	}, nil
}

func ScaleUp() {
	session, err := awsSession.NewSession(&aws.Config{Region: aws.String("us-east-1"), MaxRetries: aws.Int(10)})
	if err != nil {
		log.Fatal(err)
		return
	}
	svc := ecs.New(session)
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

	if provisioningTasksResources.CPU == 0 || provisioningTasksResources.Memory == 0 {
		log.Println("There is no tasks in PROVISIONING state. No need to scale up")
		return
	}

	cpuRatio := float64(runningTasksResources.CPU + provisioningTasksResources.CPU) / float64(provisioningTasksResources.CPU)
	memoryRatio := float64(runningTasksResources.Memory + provisioningTasksResources.Memory) / float64(provisioningTasksResources.Memory)
	scaleRatio := math.Max(cpuRatio, memoryRatio)

	autoscalingSvc := autoscaling.New(session)
	describeAutoScalingGroupsInput := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String("executor-asg")},
	}
	describeAutoScalingGroupsOutput, err := autoscalingSvc.DescribeAutoScalingGroups(describeAutoScalingGroupsInput)
	if err != nil {
		log.Println("cant describe autoscaling group", err)
		return
	}
	autoScalingGroup := describeAutoScalingGroupsOutput.AutoScalingGroups[0]
	curentInstanceCount, err := getInstanceCount(svc)
	if err != nil {
		log.Println("Error while getting instance count", err)
	}

	scaledDesiredCount := int64(math.Ceil(scaleRatio * float64(curentInstanceCount))) + curentInstanceCount
	if scaledDesiredCount > curentInstanceCount {
		if *autoScalingGroup.MaxSize < scaledDesiredCount {
			scaledDesiredCount = *autoScalingGroup.MaxSize
		}
		updateGroupInput := &autoscaling.UpdateAutoScalingGroupInput{
			AutoScalingGroupName: autoScalingGroup.AutoScalingGroupName,
			DesiredCapacity: aws.Int64(scaledDesiredCount),
		}
		_, err := autoscalingSvc.UpdateAutoScalingGroup(updateGroupInput)
		if err != nil {
			log.Println("Error while updating group", err)
		} else {
			log.Printf("Capacity updated from %d to %d \n", curentInstanceCount, scaledDesiredCount)
		}
	}
}
