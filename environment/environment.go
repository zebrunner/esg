package environment

import (
	"fmt"

	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"

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

func (env *ExecutionEnvironment) ContainerOverrides() []*ecs.ContainerOverride {
	overrides := []*ecs.ContainerOverride{}
	for _, container := range env.Containers {
		cpu := container.Cpu()
		memory := container.Memory()
		override := ecs.ContainerOverride{
			Name:    &container.Name,
			Cpu:     &cpu,
			Memory:  &memory,
			Command: aws.StringSlice(container.Command),
		}

		if strings.ToLower(env.Capabilities.PlatformName.ToPrimitive()) != envtype.WINDOWS.String() {
			override.MemoryReservation = &memory
		}

		env := []*ecs.KeyValuePair{}
		for k, v := range container.Env {
			// need to declare local variables to provide as pointer later
			key := k
			value := v
			env = append(env, &ecs.KeyValuePair{Name: &key, Value: &value})
		}
		override.Environment = env

		overrides = append(overrides, &override)
	}

	return overrides
}

func (e *ExecutionEnvironment) HashOvverideDefinition() string {
	overrideContainersData := make([]*Container, 0, len(e.Containers))

	for _, container := range e.Containers {
		dependsOn := make([]*ecs.ContainerDependency, 0)
		if container.DependsOn != nil {
			for _, dependency := range container.DependsOn {
				if dependency == nil {
					continue
				}
				dependsOn = append(dependsOn, &ecs.ContainerDependency{
					Condition:     dependency.Condition,
					ContainerName: dependency.ContainerName,
				})
			}
		}

		var healthCheck *ecs.HealthCheck
		if container.HealthCheck != nil {
			healthCheck = &ecs.HealthCheck{
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

func (env *ExecutionEnvironment) ContainerDefinitions() []*ecs.ContainerDefinition {
	definitions := []*ecs.ContainerDefinition{}

	for _, c := range env.Containers {
		cpu := c.Cpu()
		memory := c.Memory()
		imageUrl := c.getImageUrl()
		containerDefinition := ecs.ContainerDefinition{
			Name:        &c.Name,
			Image:       &imageUrl,
			Cpu:         &cpu,
			Memory:      &memory,
			Essential:   &c.Essential,
			HealthCheck: c.HealthCheck,
			DependsOn:   c.DependsOn,
			Links:       aws.StringSlice(c.Links),
			EntryPoint:  aws.StringSlice(c.EntryPoint),
		}

		if strings.ToLower(env.Capabilities.PlatformName.ToPrimitive()) != envtype.WINDOWS.String() {
			containerDefinition.MemoryReservation = &memory
			containerDefinition.Privileged = &c.Privileged
		}

		if c.WorkingDirectory != "" {
			containerDefinition.WorkingDirectory = &c.WorkingDirectory
		}

		if env.AwsLogsGroup != "" {
			streamPrefix := env.Type.String()

			containerDefinition.LogConfiguration = &ecs.LogConfiguration{
				LogDriver: aws.String("awslogs"),
				Options: map[string]*string{
					"awslogs-group":         &env.AwsLogsGroup,
					"awslogs-region":        &config.Conf.AwsRegion,
					"awslogs-stream-prefix": &streamPrefix,
				},
			}
		}

		volumes := []*ecs.MountPoint{}
		for _, volumeName := range c.Mounts {
			// local declarations required to append all values
			volume := env.Volumes[volumeName]
			name := volumeName
			containerPath := volume.ContainerPath
			readOnly := volume.ReadOnly
			volumes = append(volumes, &ecs.MountPoint{
				ContainerPath: &containerPath,
				SourceVolume:  &name,
				ReadOnly:      &readOnly,
			})
		}
		containerDefinition.MountPoints = volumes

		portMappings := []*ecs.PortMapping{}
		for _, mapping := range c.Ports {
			m := ecs.PortMapping{
				ContainerPort: aws.Int64(mapping.ContainerPort),
				HostPort:      aws.Int64(mapping.HostPort),
			}
			portMappings = append(portMappings, &m)
		}
		containerDefinition.PortMappings = portMappings

		if env.Type == envtype.GENERIC {
			env := []*ecs.KeyValuePair{}
			for k, v := range c.Env {
				// need to declare local variables to provide as pointer later
				key := k
				value := v
				env = append(env, &ecs.KeyValuePair{Name: &key, Value: &value})
			}

			containerDefinition.Environment = env
			containerDefinition.Command = aws.StringSlice(c.Command)
		}

		definitions = append(definitions, &containerDefinition)
	}

	return definitions
}

func (env *ExecutionEnvironment) Volume() []*ecs.Volume {
	volumes := []*ecs.Volume{}
	for n, v := range env.Volumes {
		if v.HostPath != "" {
			volumes = append(volumes, &ecs.Volume{
				Host: &ecs.HostVolumeProperties{
					SourcePath: aws.String(v.HostPath),
				},
				Name: aws.String(n),
			})
		} else {
			volumes = append(volumes, &ecs.Volume{
				DockerVolumeConfiguration: &ecs.DockerVolumeConfiguration{
					Driver: aws.String(v.Driver),
					Scope:  aws.String(v.Scope),
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
		dependsOn := make([]*ecs.ContainerDependency, 0)
		if container.DependsOn != nil {
			for _, dependency := range container.DependsOn {
				if dependency == nil {
					continue
				}
				dependsOn = append(dependsOn, &ecs.ContainerDependency{
					Condition:     dependency.Condition,
					ContainerName: dependency.ContainerName,
				})
			}
		}

		var healthCheck *ecs.HealthCheck
		if container.HealthCheck != nil {
			healthCheck = &ecs.HealthCheck{
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
