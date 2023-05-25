package service

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

func GetClusterTasks(svc *ecs.ECS) ([]*ecs.Task, error) {
	tasks := []*ecs.Task{}
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.Conf.AwsCluster,
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
			Cluster: &config.Conf.AwsCluster,
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

func GetClusterTasksArn(svc *ecs.ECS) ([]*string, error) {
	taskArns := make([]*string, 0)
	listTasksInput := &ecs.ListTasksInput{
		Cluster: &config.Conf.AwsCluster,
	}

	for {
		listTasksResult, err := svc.ListTasks(listTasksInput)
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

func GetSessionMapTasks(keys []string, svc *ecs.ECS) []*ecs.Task {
	// Construct pages of *string with 100 or fewer elements for requests. 100 is an AWS limitation for Describe* requests
	pages := paginate(aws.StringSlice(keys), 100)
	tasks := make([]*ecs.Task, 0)
	// Send DescribeTasks requests and save response tasks into array
	for _, tasksPage := range pages {
		if len(tasksPage) == 0 {
			break
		}

		describeTasksInput := ecs.DescribeTasksInput{
			Cluster: &config.Conf.AwsCluster,
			Tasks:   tasksPage,
		}
		output, err := svc.DescribeTasks(&describeTasksInput)
		if err != nil {
			log.WithError(err).Error("Failed to describe tasks!")
		}
		tasks = append(tasks, output.Tasks...)
	}

	return tasks
}
