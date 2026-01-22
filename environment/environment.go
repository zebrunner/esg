package environment

import (
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"github.com/zebrunner/esg/cachemaps/definitionmap"
	"github.com/zebrunner/esg/cachemaps/resourcesToAllocate"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/utils"
)

type ExecutionEnvironment struct {
	TaskDefinitionFamily string
	Schema               string
	Type                 envtype.ENV_TYPE
	TaskRoleArn          string
	CapacityProvider     string
	Containers           []*Container
	Network              *network.NetworkConfiguration
	Volumes              map[string]volume
	Capabilities         *capabilities.Capabilities
	AwsLogsGroup         string
}

func (env *ExecutionEnvironment) ContainerOverrides() []ecsTypes.ContainerOverride {
	overrides := []ecsTypes.ContainerOverride{}
	for _, container := range env.Containers {
		cpu := int32(container.Cpu())
		memory := int32(container.Memory())
		override := ecsTypes.ContainerOverride{
			Name:   aws.String(container.Name),
			Cpu:    &cpu,
			Memory: &memory,
		}

		if strings.ToLower(env.Capabilities.PlatformName.ToPrimitive()) != envtype.WINDOWS.String() {
			override.MemoryReservation = &memory
		}

		// Env vars and command var are passed on task definition register phase for generic env due to:
		// Task definition ovveride max symbols num constraint:
		// InvalidParameterException: Container Overrides length must be at most 8192.
		// When task definition register max symbols constraint is much higher:
		// ClientException: Actual length: '117374'. Max allowed length is '65536' bytes.
		// It is also possible because we always register new generic task definition before it starts
		if env.Type != envtype.GENERIC {
			override.Command = container.Command
			envVars := []ecsTypes.KeyValuePair{}
			for k, v := range container.Env {
				envVars = append(envVars, ecsTypes.KeyValuePair{
					Name:  aws.String(k),
					Value: aws.String(v),
				})
			}
			override.Environment = envVars
		}

		overrides = append(overrides, override)
	}

	return overrides
}

func (e *ExecutionEnvironment) HashOvverideDefinition() string {
	overrideContainersData := make([]*Container, 0, len(e.Containers))

	for _, container := range e.Containers {
		dependsOn := make([]ecsTypes.ContainerDependency, 0)
		if container.DependsOn != nil {
			for _, dependency := range container.DependsOn {
				dependsOn = append(dependsOn, ecsTypes.ContainerDependency{
					Condition:     dependency.Condition,
					ContainerName: dependency.ContainerName,
				})
			}
		}

		var healthCheck *ecsTypes.HealthCheck
		if container.HealthCheck != nil {
			healthCheck = &ecsTypes.HealthCheck{
				Command:     container.HealthCheck.Command,
				Interval:    container.HealthCheck.Interval,
				Retries:     container.HealthCheck.Retries,
				StartPeriod: container.HealthCheck.StartPeriod,
				Timeout:     container.HealthCheck.Timeout,
			}
		}

		c := &Container{
			Name:             container.Name,
			Image:            container.getImageNameTag(),
			Essential:        container.Essential,
			Privileged:       container.Privileged,
			Ports:            container.Ports,
			Mounts:           container.Mounts,
			Links:            container.Links,
			EntryPoint:       container.EntryPoint,
			WorkingDirectory: container.WorkingDirectory,
			HealthCheck:      healthCheck,
			DependsOn:        dependsOn,
		}

		overrideContainersData = append(overrideContainersData, c)
	}

	overrideDefinitionData := &ExecutionEnvironment{
		TaskDefinitionFamily: e.TaskDefinitionFamily,
		Containers:           overrideContainersData,
	}

	overrideDefinitionHash := utils.EncodeToHash(overrideDefinitionData)

	return overrideDefinitionHash
}

func (env *ExecutionEnvironment) ContainerDefinitions() []ecsTypes.ContainerDefinition {
	definitions := []ecsTypes.ContainerDefinition{}

	for _, c := range env.Containers {
		cpu := int32(c.Cpu())
		memory := int32(c.Memory())
		imageUrl := c.getImageUrl()
		containerDefinition := ecsTypes.ContainerDefinition{
			Name:                   aws.String(c.Name),
			Image:                  aws.String(imageUrl),
			Cpu:                    cpu,
			Memory:                 &memory,
			Essential:              aws.Bool(c.Essential),
			HealthCheck:            c.HealthCheck,
			DependsOn:              c.DependsOn,
			Links:                  c.Links,
			EntryPoint:             c.EntryPoint,
			ReadonlyRootFilesystem: aws.Bool(c.ReadOnlyRootFileSystem),
		}

		if strings.ToLower(env.Capabilities.PlatformName.ToPrimitive()) != envtype.WINDOWS.String() {
			containerDefinition.MemoryReservation = &memory
			containerDefinition.Privileged = aws.Bool(c.Privileged)
		}

		if c.WorkingDirectory != "" {
			containerDefinition.WorkingDirectory = aws.String(c.WorkingDirectory)
		}

		if env.AwsLogsGroup != "" {
			streamPrefix := env.Type.String()

			containerDefinition.LogConfiguration = &ecsTypes.LogConfiguration{
				LogDriver: ecsTypes.LogDriverAwslogs,
				Options: map[string]string{
					"awslogs-group":         env.AwsLogsGroup,
					"awslogs-region":        config.Conf.AwsRegion,
					"awslogs-stream-prefix": streamPrefix,
				},
			}
		}

		volumes := []ecsTypes.MountPoint{}
		for _, volumeName := range c.Mounts {
			volume := env.Volumes[volumeName]
			volumes = append(volumes, ecsTypes.MountPoint{
				ContainerPath: aws.String(volume.ContainerPath),
				SourceVolume:  aws.String(volumeName),
				ReadOnly:      aws.Bool(volume.ReadOnly),
			})
		}
		containerDefinition.MountPoints = volumes

		portMappings := []ecsTypes.PortMapping{}
		for _, mapping := range c.Ports {
			portMappings = append(portMappings, ecsTypes.PortMapping{
				ContainerPort: aws.Int32(int32(mapping.ContainerPort)),
				HostPort:      aws.Int32(int32(mapping.HostPort)),
			})
		}
		containerDefinition.PortMappings = portMappings

		// Env vars and command var are passed on task definition register phase for generic env due to:
		// Task definition ovveride max symbols num constraint:
		// InvalidParameterException: Container Overrides length must be at most 8192.
		// When task definition register max symbols constraint is much higher:
		// ClientException: Actual length: '117374'. Max allowed length is '65536' bytes.
		// It is also possible because we always register new generic task definition before it starts
		if env.Type == envtype.GENERIC {
			envVars := []ecsTypes.KeyValuePair{}
			for k, v := range c.Env {
				envVars = append(envVars, ecsTypes.KeyValuePair{
					Name:  aws.String(k),
					Value: aws.String(v),
				})
			}

			containerDefinition.Environment = envVars
			containerDefinition.Command = c.Command
		}

		definitions = append(definitions, containerDefinition)
	}

	return definitions
}

func (env *ExecutionEnvironment) Volume() []ecsTypes.Volume {
	volumes := []ecsTypes.Volume{}
	for n, v := range env.Volumes {
		if v.HostPath != "" {
			volumes = append(volumes, ecsTypes.Volume{
				Host: &ecsTypes.HostVolumeProperties{
					SourcePath: aws.String(v.HostPath),
				},
				Name: aws.String(n),
			})
		} else {
			var scope ecsTypes.Scope
			switch v.Scope {
			case "task":
				scope = ecsTypes.ScopeTask
			case "shared":
				scope = ecsTypes.ScopeShared
			default:
				scope = ecsTypes.ScopeTask
			}

			volumes = append(volumes, ecsTypes.Volume{
				DockerVolumeConfiguration: &ecsTypes.DockerVolumeConfiguration{
					Driver: aws.String(v.Driver),
					Scope:  scope,
				},
				Name: aws.String(n),
			})
		}
	}

	return volumes
}

func (env *ExecutionEnvironment) HashRegisterDefinition() string {
	containers := make([]*Container, 0)
	for _, container := range env.Containers {
		dependsOn := make([]ecsTypes.ContainerDependency, 0)
		if container.DependsOn != nil {
			for _, dependency := range container.DependsOn {
				dependsOn = append(dependsOn, ecsTypes.ContainerDependency{
					Condition:     dependency.Condition,
					ContainerName: dependency.ContainerName,
				})
			}
		}

		var healthCheck *ecsTypes.HealthCheck
		if container.HealthCheck != nil {
			healthCheck = &ecsTypes.HealthCheck{
				Command:     container.HealthCheck.Command,
				Interval:    container.HealthCheck.Interval,
				Retries:     container.HealthCheck.Retries,
				StartPeriod: container.HealthCheck.StartPeriod,
				Timeout:     container.HealthCheck.Timeout,
			}
		}

		c := &Container{
			Name:             container.Name,
			Image:            container.getImageUrl(),
			Res:              container.Res,
			Essential:        container.Essential,
			Privileged:       container.Privileged,
			Ports:            container.Ports,
			Mounts:           container.Mounts,
			Links:            container.Links,
			EntryPoint:       container.EntryPoint,
			WorkingDirectory: container.WorkingDirectory,
			HealthCheck:      healthCheck,
			DependsOn:        dependsOn,
		}

		containers = append(containers, c)
	}

	registerDefinitionData := &ExecutionEnvironment{
		Containers:   containers,
		Volumes:      env.Volumes,
		TaskRoleArn:  env.TaskRoleArn,
		AwsLogsGroup: env.AwsLogsGroup,
	}
	registerDefinitionHash := utils.EncodeToHash(registerDefinitionData)

	return registerDefinitionHash
}

func (env *ExecutionEnvironment) GetAllocationResources(routerUUID string) *resourcesToAllocate.ResourcesToAllocate {
	containersResources := SumResources(env.Containers)

	return &resourcesToAllocate.ResourcesToAllocate{
		RouterUUID:       routerUUID,
		Cpu:              containersResources.Cpu,
		Memory:           containersResources.Memory,
		CapacityProvider: env.CapacityProvider,
	}
}

func (env *ExecutionEnvironment) GetFamilyRevision() (string, error) {
	// used Contains() as task definition family could be org-generic/dev-generic etc.
	if strings.Contains(env.TaskDefinitionFamily, "generic") {
		return env.TaskDefinitionFamily, nil
	}

	revision, found := definitionmap.FindRevision(env.HashOvverideDefinition())
	if !found {
		return "", fmt.Errorf("revision not found for '%s'", env.TaskDefinitionFamily)
	}

	return fmt.Sprint(env.TaskDefinitionFamily, ":", revision), nil
}
