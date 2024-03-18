package utils

import "github.com/aws/aws-sdk-go/service/ecs"

func IsTaskFinishedSuccessfully(task *ecs.Task) (bool, *ecs.Container) {
	for _, container := range task.Containers {
		// if container's exit code is nil it means that container doesn't even started
		if container.ExitCode == nil || *container.ExitCode != 0 {
			return false, container
		}
	}

	return true, nil
}
