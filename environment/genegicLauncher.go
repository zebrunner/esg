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
        genericPort   int64 = 22

	minCpu    = 256
	minMemory = 256
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

        //executorImage := "maven:3.8-openjdk-11"
        if caps.Image == "" {
                return nil, fmt.Errorf("Executor container image is not specified! Image='%s'", caps.Image)
        }
        executorImage := caps.Image
        fmt.Printf("executorImage: %s\n", executorImage)


	cloneCommand := fmt.Sprintf("git clone --verbose --progress --depth=1 %s %s %s", branchArg, caps.RepositoryUrl, sharedFolder)
	postCloneCommand := ""
	if strings.HasPrefix(executorImage, "public.ecr.aws/zebrunner/cypress-") {
		postCloneCommand = " ; chown -R 4096:4096 " + sharedFolder + " ; ls -la " + sharedFolder
	}
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
                Mounts: []string{taskVolume},
                Command: []string{"-c", cloneCommand + postCloneCommand},
                EntryPoint: []string{"/bin/sh"},
        }


        if caps.LaunchCommand == "" {
                return nil, fmt.Errorf("Executor container launch command is not specified! LaunchCommand='%s'", caps.LaunchCommand)
        }
	launchCommand := caps.LaunchCommand
        fmt.Printf("launchCommand: %s\n", launchCommand)

	// -v /opt/zebrunner/cypress/start-capture-artifacts.sh:/opt/start-capture-artifacts.sh
	// -v /opt/zebrunner/cypress/stop-capture-artifacts.sh:/opt/stop-capture-artifacts.sh
	// -v /opt/zebrunner/cypress/upload-artifacts.sh:/opt/upload-artifacts.sh
	// -v /opt/zebrunner/aws:/opt/aws
	startRecordingVolume := "start-capture-artifacts"
        stopRecordingVolume := "stop-capture-artifacts"
        uploadRecordingVolume := "upload-artifacts"
        awsCliInstallerVolume := "awsCliInstaller"

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
		Mounts: []string{taskVolume, startRecordingVolume, stopRecordingVolume, uploadRecordingVolume, awsCliInstallerVolume},
		Command: []string{"-c", launchCommand},
		WorkingDirectory: sharedFolder,
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

        if strings.HasPrefix(executorImage, "public.ecr.aws/zebrunner/cypress-") {
                executorContainer.SetCpu(2048)
                executorContainer.SetMemory(4096)
                executorContainer.SetMemoryReservation(4096)
        }


        postImage := imageRepo + "post-executor:1.0"
        postContainer := Container{
                Name:              "post-executor",
                Image:             postImage,
                cpu:               minCpu,
                memory:            minMemory,
                memoryReservation: minMemory,
                Privileged: false,
                Essential:  false,
                Ports: map[string]portMapping{
                        "driver":         {genericPort, 0},
                },

                Env: map[string]string{
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.AwsRegion,
                },
                Mounts: []string{taskVolume, dockerSocketVolume},
                HealthCheck: &ecs.HealthCheck{
                        Command:     []*string{aws.String("CMD-SHELL"), aws.String("ps -ef | grep docker | grep logs || exit 1")},
                        Interval:    aws.Int64(10),
                        Retries:     aws.Int64(3),
                        Timeout:     aws.Int64(10),
                        StartPeriod: aws.Int64(5),
                },
                Links:       []string{"executor"},
                DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
                                ContainerName: aws.String("clone"),
                                Condition:  aws.String("COMPLETE"),
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
                        startRecordingVolume: {ContainerPath: "/opt/start-capture-artifacts.sh", HostPath: "/opt/zebrunner/cypress/start-capture-artifacts.sh", ReadOnly: false},
                        stopRecordingVolume: {ContainerPath: "/opt/stop-capture-artifacts.sh", HostPath: "/opt/zebrunner/cypress/stop-capture-artifacts.sh", ReadOnly: false},
                        uploadRecordingVolume: {ContainerPath: "/opt/upload-artifacts.sh", HostPath: "/opt/zebrunner/cypress/upload-artifacts.sh", ReadOnly: false},
                        awsCliInstallerVolume: {ContainerPath: "/opt/aws", HostPath: "/opt/zebrunner/aws", ReadOnly: false},
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
