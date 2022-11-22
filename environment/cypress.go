package environment

import (
        "github.com/aws/aws-sdk-go/aws"
        "github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	"fmt"
	"strings"

        log "github.com/sirupsen/logrus"
)

func buildCypress(workspace string, caps *capabilities.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	workDir := "/tmp/zebrunner"
	taskVolume := "work"

	logDir := "/tmp/log"
	logVolume := "log"

	entrypointDir := "/opt/entrypoint"
	entrypointVolume := "entrypoint"

        cypressDir := "/opt/cypress"
	cypressVolume := "cypress"

	zebrunnerDir := "/opt/zebrunner"
        zebrunnerVolume := "zebrunner"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch
	}

        browserImage, err := buildImage(caps)
        if err != nil {
                return nil, err
        }
	log.Debug("browserImage: " + browserImage)

	cloneCommand := "CHANGE_ME"
        taskLogRedirect :=  ">>" + logDir + "/task.log 2>&1"
        if caps.RepositoryUrl != "" {
		cloneCommand = fmt.Sprintf("git clone --verbose --progress --depth=1 %s %s %s", branchArg, caps.RepositoryUrl, workDir)
		cloneCommand = cloneCommand + taskLogRedirect + " ; chown -R 4096:4096 " + workDir + "; chown -R 4096:4096 " + logDir
	        //fmt.Printf("cloneCommand: %s\n", cloneCommand)
        }


        cloneImage := imageRepo + "git:latest"
        cloneContainer := Container{
                Name:              "clone",
                Image:             cloneImage,
                cpu:               minCpu,
                memory:            minMemory,
                memoryReservation: minMemory,
                Privileged:        false,
                Essential:         false,
                Mounts: []string{taskVolume, logVolume},
                Command: []string{"-c", cloneCommand + taskLogRedirect},
                EntryPoint: []string{"/bin/sh"},
        }

	launchCommand := "CHANGE_ME"
        if caps.LaunchCommand != "" {
                launchCommand = caps.LaunchCommand
        }

        entrypointImage := imageRepo + "entrypoint:1.0"
        entrypointContainer := Container{
                Name:              "entrypoint",
                Image:             entrypointImage,
                cpu:               minCpu,
                memory:            minMemory,
                memoryReservation: minMemory,
                Privileged:        false,
                Essential:         false,
                Mounts: []string{entrypointVolume, cypressVolume},
                EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
        }

	executorContainer := Container{
		Name:       "executor",
		Image:      browserImage,
		Privileged: false,
		Essential:  true,
                Env: map[string]string{
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.AwsRegion,
			"COMMAND":		  launchCommand,
                },
		Mounts: []string{entrypointVolume, taskVolume, logVolume, zebrunnerVolume, cypressVolume},
		WorkingDirectory: workDir,
                EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
                DependsOn: []*ecs.ContainerDependency{
                        &ecs.ContainerDependency{
                                ContainerName: aws.String("entrypoint"),
                                Condition:  aws.String("COMPLETE"),
                        },
			&ecs.ContainerDependency{
				ContainerName: aws.String("clone"),
				Condition:  aws.String("COMPLETE"),
        	        },
		},

	}

        if caps.EnvVariables != nil {
 		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			executorContainer.Env[v] = k
		}
        }

	executorContainer.SetCpu(caps.Cpu)
	executorContainer.SetMemory(caps.Memory)
	executorContainer.SetMemoryReservation(caps.MemoryReservation)

	//TODO: remove hardcoded cpu/memory
        executorContainer.SetCpu(2048)
        executorContainer.SetMemory(4096)
        executorContainer.SetMemoryReservation(4096)

        // convert image "public.ecr.aws/zebrunner/cypress-chrome:107.0" to task definition failiy: "cypress-cypress-chrome-107-0"
        familyDefinition := strings.Replace(browserImage, imageRepo, "", -1)
        familyDefinition = strings.Replace(familyDefinition, ":", "-", -1)
        familyDefinition = strings.Replace(familyDefinition, ".", "-", -1)
        log.Debug("Overidden TaskDefinitionFamily for cypress: " + familyDefinition)

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: familyDefinition,
		Containers:           []*Container{&cloneContainer, &entrypointContainer, &executorContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume: {Driver: "local", Scope: "task", ContainerPath: workDir, ReadOnly: false},
                        logVolume: {Driver: "local", Scope: "task", ContainerPath: logDir, ReadOnly: false},
			entrypointVolume: {Driver: "local", Scope: "task", ContainerPath: entrypointDir, ReadOnly: false},
                        cypressVolume: {Driver: "local", Scope: "task", ContainerPath: cypressDir, ReadOnly: false},
			zebrunnerVolume: {HostPath: zebrunnerDir, ContainerPath: zebrunnerDir, ReadOnly: true},
		},
                Network: &NetworkConfiguration{
                        IP: "",
                        Endpoints: map[string]*Endpoint{
                                "driver":      {ContainerPort: genericPort, HostPort: 0, Path: "/"},
                        },
                },

	}

	return &environment, nil
}
