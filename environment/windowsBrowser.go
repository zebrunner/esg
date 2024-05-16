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

	caps.EnableVNC = false
	caps.EnableVideo = false

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}
	logDir := "C:/Users/ContainerAdministrator/Downloads"
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
			"LOG_DIR":   logDir,
			"TASK_LOG":  "task.log",
			"LOG_FILE":  "session.log",
			"LOG_LEVEL": "INFO",
		},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("cmd.exe"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(0),
		},
	}

	recorderContainer := Container{
		Name:  "recorder",
		Image: winRecorderImage,
		Res: Resources{
			Cpu:    8,
			Memory: 8,
		},
		Privileged: false,
		Essential:  false,
		Ports: map[string]portMapping{
			"recorder": {recorderdPort, 0},
		},
		Env: map[string]string{
			"ROUTER_UUID": routerUUID,
			"LOG_DIR":     logDir,
			"TASK_LOG":    logDir + "/" + "task.log",
			"LOG_FILE":    "session.log",
		},
		Mounts: []string{logVolume},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String(fmt.Sprintf("curl -f localhost:%v/ || exit 1", recorderdPort))},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(2),
		},
		DependsOn: []*ecs.ContainerDependency{
			{
				Condition:     aws.String("START"),
				ContainerName: &browserContainer.Name,
			},
		},
	}

	uploaderContainer := Container{
		Name:  "uploader",
		Image: winUploaderImage,
		Res: Resources{
			Cpu:    16,
			Memory: 16,
		},
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"LOG_DIR":               logDir,
			"S3_BUCKET":             conf.S3Bucket,
			"S3_KEY":                fmt.Sprintf("%s/artifacts/test-sessions", workspace),
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
				"driver":        {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"healthcheck":   {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"recorderStart": {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
				"recorderStop":  {ContainerPort: recorderdPort, HostPort: 0, Path: "/stop"},
			},
		},
		Workspace:        workspace,
		RouterUUID:       routerUUID,
		CapacityProvider: config.Conf.AwsWinCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	err = calculateResources(&environment,
		&resourceCalculatorHelper{
			MinimumRes: Resources{Cpu: 1024, Memory: 1024},
			Container:  &browserContainer,
			Memory:     &caps.Memory,
			Cpu:        &caps.Cpu,
		},
	)

	if err != nil {
		return nil, err
	}

	return &environment, nil
}
