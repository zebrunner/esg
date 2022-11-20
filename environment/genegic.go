package environment

import (
        "github.com/aws/aws-sdk-go/aws"
        "github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	"fmt"
//	"strings"
)

func buildGeneric(workspace string, caps *capabilities.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	workingDir := "/tmp/zebrunner"
	taskVolume := "work"

	loggingDir := "/tmp/log"
	logVolume := "log"

        // -v /opt/zebrunner:/opt/zebrunner
        zebrunnerVolume := "zebrunner"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch
	}

        if caps.RepositoryUrl == "" {
                return nil, fmt.Errorf("Executor repository is not specified! RepositoryUrl='%s'", caps.RepositoryUrl)
        }

        //executorImage := "maven:3.8-openjdk-11"
        if caps.Image == "" {
                return nil, fmt.Errorf("Executor container image is not specified! Image='%s'", caps.Image)
        }
        executorImage := caps.Image
        //fmt.Printf("executorImage: %s\n", executorImage)


	cloneCommand := fmt.Sprintf("git clone --verbose --progress --depth=1 %s %s %s", branchArg, caps.RepositoryUrl, workingDir)
	//fmt.Printf("cloneCommand: %s\n", cloneCommand)

	taskLogRedirect :=  ">>" + loggingDir + "/task.log 2>&1"

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


        if caps.LaunchCommand == "" {
                return nil, fmt.Errorf("Executor container launch command is not specified! LaunchCommand='%s'", caps.LaunchCommand)
        }
	launchCommand := caps.LaunchCommand

        // install as cli on executor container start
        preLaunchCommand := ZEBRUNNER_HOME + "/generic/pre-launch.sh" + " && "
	postLaunchCommand := "; " + ZEBRUNNER_HOME + "/generic/post-launch.sh"

	executorContainer := Container{
		Name:       "executor",
		Image:      executorImage,
		Privileged: false,
		Essential:  true,
                Env: map[string]string{ // aws integration required by cypress images to upload recordings per spec/feature
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.AwsRegion,
                },
		Mounts: []string{taskVolume, logVolume, zebrunnerVolume},
		Command: []string{"-c", preLaunchCommand + launchCommand + taskLogRedirect + postLaunchCommand},
		WorkingDirectory: workingDir,
		EntryPoint: []string{"/bin/sh"},
                DependsOn: []*ecs.ContainerDependency{
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

	//TODO: remove hardcoded increased resources as only reporting allow to adjust it on UI
        if executorImage == "amancevice/pandas:1.1.4" {
                executorContainer.SetCpu(4096)
                executorContainer.SetMemory(8192)
                executorContainer.SetMemoryReservation(8192)
        }

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&cloneContainer, &executorContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume: {Driver: "local", Scope: "task", ContainerPath: workingDir, ReadOnly: false},
                        logVolume: {Driver: "local", Scope: "task", ContainerPath: loggingDir, ReadOnly: false},
			zebrunnerVolume: {HostPath: "/opt/zebrunner", ContainerPath: "/opt/zebrunner", ReadOnly: true},
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
