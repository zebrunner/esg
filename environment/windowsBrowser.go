package environment

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"
)

func buildWindowsBrowser(workspace string, routerUUID string, image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	caps.EnableVNC = false
	caps.EnableVideo = false

	logDir := "C:/Users/ContainerAdministrator/Downloads"
	logVolume := "log"

	log.Trace("caps: ", caps)

	// geckodriver expects lowercase log levels (info, debug, trace)
	// chrome/edge use uppercase (INFO, DEBUG)
	logLevel := "INFO"
	if caps.BrowserName == "firefox" {
		logLevel = "info"
	}

	browserContainer := Container{
		Name:      "browser",
		image:     &image,
		Essential: true,
		Ports: map[string]portMapping{
			"driver": {ContainerPort: seleniumPort, HostPort: 0},
		},
		Mounts: []string{logVolume},
		Env: map[string]string{
			"LOG_DIR":   logDir,
			"TASK_LOG":  "task.log",
			"LOG_FILE":  "session.log",
			"LOG_LEVEL": logLevel,
		},
		HealthCheck: &ecsTypes.HealthCheck{
			Command:     []string{"cmd.exe", "curl -f localhost:4444/status || exit 1"},
			Interval:    aws.Int32(5),
			Retries:     aws.Int32(4),
			Timeout:     aws.Int32(5),
			StartPeriod: aws.Int32(0),
		},
	}

	if caps.BrowserName == "firefox" {
		browserContainer.Env["DRIVER_ARGS"] = "--allow-hosts localhost"
		browserContainer.Env["MOZ_WEBRENDER"] = "0"
		browserContainer.Env["MOZ_DISABLE_GPU_SANDBOX"] = "1"
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
			"LOG_LEVEL":   config.Conf.RecorderLogLvl,
			"LOG_FILE":    "session.log",
		},
		Mounts: []string{logVolume},
		HealthCheck: &ecsTypes.HealthCheck{
			Command:     []string{"CMD-SHELL", fmt.Sprintf("curl -f localhost:%v/ || exit 1", recorderdPort)},
			Interval:    aws.Int32(5),
			Retries:     aws.Int32(4),
			Timeout:     aws.Int32(5),
			StartPeriod: aws.Int32(2),
		},
		DependsOn: []ecsTypes.ContainerDependency{
			{
				Condition:     ecsTypes.ContainerConditionStart,
				ContainerName: aws.String(browserContainer.Name),
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
		DependsOn: []ecsTypes.ContainerDependency{
			{
				Condition:     ecsTypes.ContainerConditionStart,
				ContainerName: aws.String(browserContainer.Name),
			},
		},
	}

	containers := []*Container{&browserContainer, &recorderContainer, &uploaderContainer}

	env := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume: {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
		},
		Network: &network.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*network.Endpoint{
				"driver":        {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"healthcheck":   {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"recorderStart": {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
				"recorderStop":  {ContainerPort: recorderdPort, HostPort: 0, Path: "/stop"},
			},
		},
		Type:             envtype.WINDOWS,
		CapacityProvider: config.Conf.AwsWinCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
		AwsLogsGroup:     config.Conf.AwsLogsGroup,
	}

	if caps.BrowserName == "firefox" {
		env.Network.Endpoints["gecko_driver"] = &network.Endpoint{ContainerPort: seleniumPort, HostPort: 0, Path: "/"}
	}

	err := calculateResources(&env,
		&resourceCalculationHelper{
			MinimumRes: Resources{Cpu: 1024, Memory: 1024},
			Container:  &browserContainer,
			Memory:     &caps.Memory,
			Cpu:        &caps.Cpu,
		},
	)

	if err != nil {
		return nil, err
	}

	return &env, nil
}
