package environment

import (
        "github.com/aws/aws-sdk-go/aws"
        "github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	"fmt"
	"strings"
)

const (
	minCpu    = 128
	minMemory = 128
)

func buildGeneric(workspace string, caps *capabilities.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	sharedFolder := "/opt/zebrunner"
	taskVolume := "data"
	dockerSocketVolume := "docker-socket"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch
	}

        if caps.RepositoryUrl == "" {
                return nil, fmt.Errorf("Executor repository is not specified! RepositoryUrl='%s'", caps.RepositoryUrl)
        }

	cloneCommand := fmt.Sprintf("clone --verbose --depth=1 %s %s %s", branchArg, caps.RepositoryUrl, sharedFolder)
	fmt.Printf("cloneCommand: %s\n", cloneCommand)

        cloneImage := imageRepo + "git:latest"
        cloneContainer := Container{
                Name:              "clone",
                Image:             cloneImage,
                cpu:               minCpu,
                memory:            minMemory,
                memoryReservation: minMemory,
                Privileged:        false,
                Essential:         false,
                Env: map[string]string{
                        "VERBOSE":      "0",
                },
                Mounts: []string{taskVolume},
		Command: strings.Fields(cloneCommand),
        }


        //executorImage := "maven:3.8-openjdk-11"
        if caps.Image == "" {
                return nil, fmt.Errorf("Executor container image is not specified! Image='%s'", caps.Image)
        }
        executorImage := caps.Image
        fmt.Printf("executorImage: %s\n", executorImage)

        if caps.LaunchCommand == "" {
                return nil, fmt.Errorf("Executor container launch command is not specified! LaunchCommand='%s'", caps.LaunchCommand)
        }
	launchCommand := caps.LaunchCommand
        fmt.Printf("launchCommand: %s\n", launchCommand)

	executorContainer := Container{
		Name:       "executor",
		Image:      executorImage,
		Privileged: false,
		Essential:  false,
		Mounts: []string{taskVolume},
                Command: strings.Fields(launchCommand),
		WorkingDirectory: sharedFolder,
                DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
				Condition:  aws.String("COMPLETE"),
				ContainerName: aws.String("clone"),
        	        },
		},

	}

        if caps.EnvVariables != nil {
                fmt.Printf("EnvVariables: %v\n", caps.EnvVariables)
		executorContainer.Env = caps.EnvVariables
        }

	executorContainer.SetCpu(caps.Cpu)
	executorContainer.SetMemory(caps.Memory)
	executorContainer.SetMemoryReservation(caps.MemoryReservation)


        postImage := imageRepo + "alpine:latest"
        postContainer := Container{
                Name:              "post-executor",
                Image:             postImage,
                cpu:               minCpu,
                memory:            minMemory,
                memoryReservation: minMemory,
                Privileged: false,
                Essential:  true,
                Env: map[string]string{
                        "UUID":                   caps.LaunchId,
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.AwsRegion,
                },
                Mounts: []string{taskVolume, dockerSocketVolume},
                Links:       []string{"executor"},
                DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
                        	Condition:  aws.String("START"),
	                        ContainerName: aws.String("executor"),
	                },
		},
        }


	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&cloneContainer, &executorContainer, &postContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume: {ContainerPath: sharedFolder, Driver: "local", Scope: "task", ReadOnly: false},
                        dockerSocketVolume: {ContainerPath: "/var/run/docker.sock", HostPath: "/var/run/docker.sock", ReadOnly: false},
		},
	}

	return &environment, nil
}
