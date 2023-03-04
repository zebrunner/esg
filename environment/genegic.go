package environment

import (
        "github.com/aws/aws-sdk-go/aws"
        "github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	"fmt"
	b64 "encoding/base64"
)

func buildGeneric(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
        conf := &config.Conf

	workDir := "/tmp/zebrunner"
	taskVolume := "work"

	logDir := "/tmp/log"
	logVolume := "log"

	entrypointDir := "/opt/entrypoint"
	entrypointVolume := "entrypoint"

	zebrunnerDir := "/opt/zebrunner"
        zebrunnerVolume := "zebrunner"

	mavenDir := "/root/.m2/repository"
	mavenVolume := "maven"

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


	cloneCommand := fmt.Sprintf("git clone --depth=1 %s %s %s", branchArg, caps.RepositoryUrl, workDir)
	//fmt.Printf("cloneCommand: %s\n", cloneCommand)

	taskLogRedirect :=  ">>" + logDir + "/task.log 2>&1"

        cloneImage := imageRepo + "git:latest"
        cloneContainer := Container{
                Name:              "clone",
                Image:             cloneImage,
                cpu:               minCpu,
                memory:            512, //increased memory to fix OOM for huge repositories (3K+ branches)
                Privileged:        false,
                Essential:         false,
                Mounts: []string{taskVolume, logVolume},
                Command: []string{"-c", cloneCommand + taskLogRedirect},
                EntryPoint: []string{"/bin/sh"},
        }

        entrypointImage := imageRepo + "entrypoint:1.4-beta1"
        entrypointContainer := Container{
                Name:              "entrypoint",
                Image:             entrypointImage,
                cpu:               minCpu,
                memory:            minMemory,
                Privileged:        false,
                Essential:         false,
                Mounts: []string{entrypointVolume},
                EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
        }


        mavenImage := imageRepo + "m2-repo-carina:1.1"
        mavenContainer := Container{
                Name:              "maven",
                Image:             mavenImage,
                cpu:               minCpu,
                memory:            minMemory,
                Privileged:        false,
                Essential:         false,
                Mounts: []string{mavenVolume},
        }

        if caps.LaunchCommand == "" {
                return nil, fmt.Errorf("Executor container launch command is not specified! LaunchCommand='%s'", caps.LaunchCommand)
        }
	launchCommand := caps.LaunchCommand

	//basic auth header for executor-logs service
	basicAuthHeader := "Authorization: Basic " + b64.StdEncoding.EncodeToString([]byte(conf.ZebrunnerIntegrationUser + ":" + conf.ZebrunnerIntegrationPassword))

	executorContainer := Container{
		Name:       "executor",
		Image:      executorImage,
		Privileged: false,
		Essential:  true,
                Env: map[string]string{
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.S3AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.S3AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.S3Region,
			"COMMAND":		  launchCommand,
			"BASIC_AUTH":             basicAuthHeader,
                },
		Mounts: []string{entrypointVolume, taskVolume, logVolume, zebrunnerVolume, mavenVolume},
                WorkingDirectory: workDir,
                EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
                DependsOn: []*ecs.ContainerDependency{
                        &ecs.ContainerDependency{
                                ContainerName: aws.String("maven"),
                                Condition:  aws.String("COMPLETE"),
                        },
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

	executorContainer.SetCpu(caps)
	executorContainer.SetMemory(caps)

	//TODO: remove hardcoded increased resources as only reporting allow to adjust it on UI
        if executorImage == "amancevice/pandas:1.1.4" {
                caps.Cpu = 2048
                executorContainer.SetCpu(caps)
                caps.Memory = 4096
                executorContainer.SetMemory(caps)
        }

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&cloneContainer, &entrypointContainer, &mavenContainer, &executorContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume: {Driver: "local", Scope: "task", ContainerPath: workDir, ReadOnly: false},
                        logVolume: {Driver: "local", Scope: "task", ContainerPath: logDir, ReadOnly: false},
                        entrypointVolume: {Driver: "local", Scope: "task", ContainerPath: entrypointDir, ReadOnly: false},
                        mavenVolume: {Driver: "local", Scope: "task", ContainerPath: mavenDir, ReadOnly: false},
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
