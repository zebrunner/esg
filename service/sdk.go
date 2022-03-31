package service

import (
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
