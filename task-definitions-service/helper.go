package definitionsservice

import (
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/service"
)

func getTaskDefinitionId(configuration *environment.ExecutionEnvironment) (id *int64, err error) {
	taskDefinition, err := service.CreateTaskDefinition(
		configuration.ContainerDefinitions(),
		configuration.Volume(),
		configuration.TaskDefinitionFamily,
		configuration.TaskRoleArn,
	)
	if err != nil {
		return
	}
	return taskDefinition.Revision, nil
}
