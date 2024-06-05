package environment

import (
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"

	b64 "encoding/base64"
	"fmt"
)

func buildGeneric(workspace string, routerUUID string, image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
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

	branch := ""
	branchArg := ""
	if caps.Branch != "" {
		branch = caps.Branch.ToPrimitive()
		branchArg = "--branch=" + caps.Branch.ToPrimitive()
	}

	if caps.RepositoryUrl == "" {
		return nil, fmt.Errorf("executor repository is not specified! RepositoryUrl='%s'", caps.RepositoryUrl)
	}

	cloneCommand := fmt.Sprintf("git clone --progress --depth=1 --single-branch %s %s %s", branchArg, caps.RepositoryUrl, workDir)
	//fmt.Printf("cloneCommand: %s\n", cloneCommand)

	taskLogRedirect := ">>" + logDir + "/task.log 2>&1"

	cloneContainer := Container{
		Name:  "clone",
		Image: cloneImage,
		Res: Resources{
			Cpu:    cloneContainerMinCpu,
			Memory: cloneContainerMinMemory,
		},
		Privileged: false,
		Essential:  false,
		Mounts:     []string{taskVolume, logVolume},
		Command:    []string{"-c", cloneCommand + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
	}

	entrypointContainer := Container{
		Name:  "entrypoint",
		Image: entrypointImage,
		Res: Resources{
			Cpu:    16,
			Memory: 16,
		},
		Privileged: false,
		Essential:  false,
		Mounts:     []string{entrypointVolume, logVolume},
		EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
	}

	includeMaven := strings.Contains(caps.Image.ToPrimitive(), "maven")
	var mavenContainer *Container = nil
	if includeMaven {
		mavenContainer = &Container{
			Name:  "maven",
			Image: mavenImage,
			Res: Resources{
				Cpu:    16,
				Memory: 16,
			},
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
	basicAuthHeader := "Basic " + b64.StdEncoding.EncodeToString([]byte(conf.ZebrunnerIntegrationUser+":"+conf.ZebrunnerIntegrationPassword))

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
		image:      &image,
		Privileged: false,
		Essential:  true,
		Env: map[string]string{
			"COMMAND": launchCommand.ToPrimitive(),
			"branch":  branch,
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

	executorContainer.Env["UUID"] = routerUUID
	executorContainer.Env["E3S_URL"] = config.Conf.AwsEsgUrl

	recorderContainer := Container{
		Name:  "recorder",
		Image: recorderImage,
		Res: Resources{
			Cpu:    16, // was 32
			Memory: 64, // was 256 // with 128 failed for cyserver "OutOfMemoryError: Container killed due to memory usage"
		},
		Privileged: false,
		Essential:  false,
		Ports: map[string]portMapping{
			"recorder": {recorderdPort, 0},
		},
		Env: map[string]string{
			"ROUTER_UUID":          routerUUID,
			"ENABLE_VIDEO":         "false",
			"ENABLE_REALTIME_LOGS": "true",
			"LOG_LEVEL":            config.Conf.RecorderLogLvl,
			"BASIC_AUTH":           basicAuthHeader,
			"LOG_FILE":             "console.log",
		},
		Mounts: []string{logVolume},
		HealthCheck: &ecs.HealthCheck{
			// check if recorder's binary process is running, no curl is downloaded inside of container
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("pgrep recorder")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(2),
		},
	}
	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			recorderContainer.Env[v] = k
		}
	}

	uploaderContainer := Container{
		Name:  "uploader",
		Image: uploaderImage,
		Res: Resources{
			Cpu:    32,
			Memory: 64,
		},
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

	env := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes:              volumes,
		Network: &network.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*network.Endpoint{
				"driver":        {ContainerPort: genericPort, HostPort: 0, Path: "/"},
				"recorderStart": {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
				"recorderStop":  {ContainerPort: recorderdPort, HostPort: 0, Path: "/stop"},
			},
		},
		Type:             envtype.GENERIC,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	err := calculateResources(&env,
		&resourceCalculationHelper{
			MinimumRes: Resources{Cpu: 1024, Memory: 1024},
			Container:  &executorContainer,
			Memory:     &caps.Memory,
			Cpu:        &caps.Cpu,
		},
	)
	if err != nil {
		return nil, err
	}

	return &env, nil
}
