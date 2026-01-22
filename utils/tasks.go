package utils

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
)

func IsTaskFinishedSuccessfully(task *ecsTypes.Task) (bool, *ecsTypes.Container) {
	for i := range task.Containers {
		container := &task.Containers[i]
		// if container's exit code is nil it means that container doesn't even started
		if container.ExitCode == nil || *container.ExitCode != 0 {
			return false, container
		}
	}

	return true, nil
}

func GetContainerExitReason(container *ecsTypes.Container) string {
	reason := ""
	if container.Name == nil {
		return reason
	}

	reason = fmt.Sprintf("Container '%s' stopped.", aws.ToString(container.Name))
	if container.ExitCode != nil {
		reason = fmt.Sprintf("%s Exit code: %v.", reason, *container.ExitCode)
	}
	if container.Reason != nil {
		reason = fmt.Sprintf("%s Reason: %v.", reason, aws.ToString(container.Reason))
	}

	return reason
}
