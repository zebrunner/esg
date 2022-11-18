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

        browserImage, err := buildImage(caps)
        if err != nil {
                return nil, err
        }
	log.Debug("browserImage: " + browserImage)

	cloneCommand := "CHANGE_ME"
        if caps.RepositoryUrl != "" {
		cloneCommand = fmt.Sprintf("git clone --verbose --progress --depth=1 %s %s %s", branchArg, caps.RepositoryUrl, workingDir)
		cloneCommand = cloneCommand + " ; chown -R 4096:4096 " + workingDir + " ; ls -la " + workingDir
	        //fmt.Printf("cloneCommand: %s\n", cloneCommand)
        }

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

	launchCommand := "CHANGE_ME"
        if caps.LaunchCommand != "" {
                launchCommand = caps.LaunchCommand
	        //fmt.Printf("launchCommand: %s\n", launchCommand)
        }

        // install as cli on executor container start
        preLaunchCommand := ZEBRUNNER_HOME + "/generic/pre-launch.sh" + " && "

	executorContainer := Container{
		Name:       "executor",
		Image:      browserImage,
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
		Command: []string{"-c", preLaunchCommand + launchCommand + taskLogRedirect},
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
