package environment

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	log "github.com/sirupsen/logrus"
)

func buildWindowsBrowser(workspace string, routerUUID string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}
	logDir := "C:\\selenium"
	logVolume := "log"

	log.Trace("caps: ", caps)

	browserContainer := Container{
		Name:      "browser",
		Image:     browserImage,
		Essential: true,
		Ports: map[string]portMapping{
			"driver": {seleniumPort, 0},
		},
		Mounts: []string{logVolume},
		Env: map[string]string{
			"LOG_DIR":  logDir,
			"TASK_LOG": "task.log",
			"LOG_FILE": "session.log",
		},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("cmd.exe"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(0),
		},
	}
	browserContainer.SetCpu(&caps.Cpu, 1024, conf.MaxCpu)
	browserContainer.SetMemory(&caps.Memory, 1024, conf.MaxMemory)

	recorderContainer := Container{
		Name:       "recorder",
		Image:      winRecorderImage,
		cpu:        recorderCpu,
		memory:     recorderMemory,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"ROUTER_UUID": routerUUID,
			"LOG_DIR":     logDir,
			"TASK_LOG":    "task.log",
			"LOG_FILE":    "session.log",
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
		DependsOn: []*ecs.ContainerDependency{
			{
				Condition:     aws.String("START"),
				ContainerName: &browserContainer.Name,
			},
		},
	}

	uploaderContainer := Container{
		Name:       "uploader",
		Image:      winUploaderImage,
		cpu:        64,  // with 32  uploading is aborted
		memory:     256, // 64 works for single thread. for backgroud copying it is not enough
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"LOG_DIR":               logDir,
			"AWS_BUCKET":            conf.S3Bucket,
			"AWS_KEY":               fmt.Sprintf("%s/artifacts/test-sessions", workspace),
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
		DependsOn: []*ecs.ContainerDependency{
			{
				Condition:     aws.String("START"),
				ContainerName: &browserContainer.Name,
			},
		},
	}

	containers := []*Container{&browserContainer, &recorderContainer, &uploaderContainer}
	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume: {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver":      {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"healthcheck": {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
			},
		},
		Workspace:        workspace,
		RouterUUID:       routerUUID,
		CapacityProvider: config.Conf.AwsWinCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	return &environment, nil
}
