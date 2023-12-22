package environment

import (
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	b64 "encoding/base64"
	"fmt"
)

func buildGeneric(workspace string, routerUUID string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	caps.EnableVNC = false
	caps.EnableVideo = false

	workDir := "/tmp/zebrunner"
	taskVolume := "work"

	logDir := "/tmp/log"
	logVolume := "log"

	entrypointDir := "/opt/entrypoint"
	entrypointVolume := "entrypoint"

	mavenDir := "/root/.m2/repository"
	mavenVolume := "maven"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch.ToPrimitive()
	}

	if caps.RepositoryUrl == "" {
		return nil, fmt.Errorf("executor repository is not specified! RepositoryUrl='%s'", caps.RepositoryUrl)
	}

	//executorImage := "maven:3.8-openjdk-11"
	if caps.Image == "" {
		return nil, fmt.Errorf("executor container image is not specified! Image='%s'", caps.Image)
	}
	executorImage := caps.Image
	//fmt.Printf("executorImage: %s\n", executorImage)

	cloneCommand := fmt.Sprintf("git clone --progress --depth=1 --single-branch %s %s %s", branchArg, caps.RepositoryUrl, workDir)
	//fmt.Printf("cloneCommand: %s\n", cloneCommand)

	taskLogRedirect := ">>" + logDir + "/task.log 2>&1"

	cloneContainer := Container{
		Name:       "clone",
		Image:      cloneImage,
		cpu:        minCpu,
		memory:     512, //increased memory to fix OOM for huge repositories (3K+ branches)
		Privileged: false,
		Essential:  false,
		Mounts:     []string{taskVolume, logVolume},
		Command:    []string{"-c", cloneCommand + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
	}

	entrypointContainer := Container{
		Name:       "entrypoint",
		Image:      entrypointImage,
		cpu:        16,
		memory:     16,
		Privileged: false,
		Essential:  false,
		Mounts:     []string{entrypointVolume, logVolume},
		EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
	}

	includeMaven := strings.Contains(caps.Image.ToPrimitive(), "maven")
	var mavenContainer *Container = nil
	if includeMaven {
		mavenContainer = &Container{
			Name:       "maven",
			Image:      mavenImage,
			cpu:        16,
			memory:     16,
			Privileged: false,
			Essential:  false,
			Mounts:     []string{mavenVolume},
		}
	}

	if caps.LaunchCommand == "" {
		return nil, fmt.Errorf("executor container launch command is not specified! LaunchCommand='%s'", caps.LaunchCommand)
	}
	launchCommand := caps.LaunchCommand

	//basic auth header for executor-logs service
	basicAuthHeader := "Authorization: Basic " + b64.StdEncoding.EncodeToString([]byte(conf.ZebrunnerIntegrationUser+":"+conf.ZebrunnerIntegrationPassword))

	mounts := []string{entrypointVolume, taskVolume, logVolume}
	if includeMaven {
		mounts = append(mounts, mavenVolume)
	}

	dependsOn := make([]*ecs.ContainerDependency, 0)
	if includeMaven {
		dependsOn = append(dependsOn, &ecs.ContainerDependency{
			ContainerName: aws.String("maven"),
			Condition:     aws.String("COMPLETE"),
		})
	}
	dependsOn = append(dependsOn, &ecs.ContainerDependency{
		ContainerName: aws.String("entrypoint"),
		Condition:     aws.String("COMPLETE"),
	})
	dependsOn = append(dependsOn, &ecs.ContainerDependency{
		ContainerName: aws.String("clone"),
		Condition:     aws.String("COMPLETE"),
	})
	executorContainer := Container{
		Name:       "executor",
		Image:      executorImage.ToPrimitive(),
		Privileged: false,
		Essential:  true,
		Env: map[string]string{
			"COMMAND": launchCommand.ToPrimitive(),
		},
		Mounts:           mounts,
		WorkingDirectory: workDir,
		// we can't redirect logs from this place to support SIGTERM detection on trap
		// actual redirection happens inside entrypoint container: https://github.com/zebrunner/entrypoint/issues/51
		Command: []string{entrypointDir + "/entrypoint.sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("exit 0")}, // Healthy as container started
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(3),
			Timeout:     aws.Int64(10),
			StartPeriod: aws.Int64(0),
		},
		DependsOn: dependsOn,
	}

	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			executorContainer.Env[v] = k
		}
	}

	executorContainer.SetCpu(&caps.Cpu, 1024, conf.MaxCpu)
	executorContainer.SetMemory(&caps.Memory, 1024, conf.MaxMemory)

	recorderContainer := Container{
		Name:       "recorder",
		Image:      recorderImage,
		cpu:        32,
		memory:     256, // with 128 failed for cyserver "OutOfMemoryError: Container killed due to memory usage"
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"ROUTER_UUID":          routerUUID,
			"ENABLE_VIDEO":         "false",
			"ENABLE_REALTIME_LOGS": "true",
			"BASIC_AUTH":           basicAuthHeader,
			"LOG_FILE":             "console.log",
		},
		Mounts:      []string{logVolume},
		Command:     []string{"-c", "/entrypoint.sh"}, // + taskLogRedirect}, //TODO: restore redirect
		EntryPoint:  []string{"/bin/sh"},
		HealthCheck: nil,
	}
	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			recorderContainer.Env[v] = k
		}
	}

	uploaderContainer := Container{
		Name:       "uploader",
		Image:      uploaderImage,
		cpu:        64,
		memory:     64,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"S3_KEY_PATTERN":        fmt.Sprintf("s3://%s/%s/artifacts/launches", conf.S3Bucket, workspace),
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
	}

	volumes := make(map[string]volume, 0)
	volumes[entrypointVolume] = volume{Driver: "local", Scope: "task", ContainerPath: entrypointDir, ReadOnly: false}
	volumes[taskVolume] = volume{Driver: "local", Scope: "task", ContainerPath: workDir, ReadOnly: false}
	volumes[logVolume] = volume{Driver: "local", Scope: "task", ContainerPath: logDir, ReadOnly: false}

	containers := []*Container{&cloneContainer, &entrypointContainer, &recorderContainer, &uploaderContainer, &executorContainer}
	if includeMaven {
		containers = append(containers, mavenContainer)
		volumes[mavenVolume] = volume{Driver: "local", Scope: "task", ContainerPath: mavenDir, ReadOnly: false}
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes:              volumes,
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver": {ContainerPort: genericPort, HostPort: 0, Path: "/"},
			},
		},
		Workspace:        workspace,
		RouterUUID:       routerUUID,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	return &environment, nil
}
