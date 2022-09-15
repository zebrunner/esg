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
	sharedVolume := "data"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch
	}

	cloneCommand := fmt.Sprintf("clone -v --depth=1 %s https://github.com/zebrunner/carina-demo.git %s", branchArg, sharedFolder)
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
                Mounts: []string{sharedVolume},
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
		Env: map[string]string{
			"VERBOSE": "1",
		},
		Mounts: []string{sharedVolume},
                Command: strings.Fields(launchCommand),
		WorkingDirectory: sharedFolder,
                DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
				Condition:  aws.String("COMPLETE"),
				ContainerName: aws.String("clone"),
        	        },
		},

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
                        "VERBOSE": "1",
			"CONTAINER": "executor", //explicitly declare container to be able to rename only in this project
                },
                Mounts: []string{sharedVolume},
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
			sharedVolume: {ContainerPath: sharedFolder, Driver: "local", Scope: "task", ReadOnly: false},
		},
	}

	return &environment, nil
}
