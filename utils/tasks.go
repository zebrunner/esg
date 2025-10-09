package utils

import (
	"fmt"

	"github.com/aws/aws-sdk-go/service/ecs"
)

func IsTaskFinishedSuccessfully(task *ecs.Task) (bool, *ecs.Container) {
	for _, container := range task.Containers {
		// if container's exit code is nil it means that container doesn't even started
		if container.ExitCode == nil || *container.ExitCode != 0 {
			return false, container
		}
	}

	return true, nil
}

func GetContainerExitReason(container *ecs.Container) string {
	reason := ""
	if container.Name == nil {
		return reason
	}

	reason = fmt.Sprintf("Container '%s' stopped.", *container.Name)
	if container.ExitCode != nil {
		reason = fmt.Sprintf("%s Exit code: %v.", reason, *container.ExitCode)
	}
	if container.Reason != nil {
		reason = fmt.Sprintf("%s Reason: %v.", reason, *container.Reason)
	}

	return reason
}

func GetAwsVpcTaskPrivateIPv4(attachments []*ecs.Attachment) string {
	for _, attachment := range attachments {
		if attachment == nil {
			continue
		}

		// Validate that this is an ENI attachment and it's attached
		if attachment.Type == nil || *attachment.Type != "ElasticNetworkInterface" {
			continue
		}

		if attachment.Status == nil || *attachment.Status != "ATTACHED" {
			continue
		}

		for _, kv := range attachment.Details {
			if kv != nil && kv.Name != nil && kv.Value != nil {
				if *kv.Name == "privateIPv4Address" {
					return *kv.Value
				}
			}
		}
	}
	return ""
}
