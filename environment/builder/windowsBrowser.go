package builder

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/images"
)

func buildWindowsBrowser(workspace string, routerUUID string, image *images.Image, caps *capabilities.Capabilities) (*environment.ExecutionEnvironment, error) {
	conf := &config.Conf

	caps.EnableVNC = false
	caps.EnableVideo = false

	logDir := "C:\\Users\\ContainerAdministrator\\Downloads"
	logVolume := "log"

	log.Trace("caps: ", caps)

	browserContainer := environment.Container{
		Name:         "browser",
		ImageIsConst: false,
		Essential:    true,
		Ports: map[string]environment.PortMapping{
			"driver": {ContainerPort: seleniumPort, HostPort: 0},
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

	if image != nil {
		browserContainer.Image = image.GetUrl()
	}

	recorderContainer := environment.Container{
		Name:         "recorder",
		Image:        winRecorderImage,
		ImageIsConst: true,
		Res: environment.Resources{
			Cpu:    8,
			Memory: 8,
		},
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

	uploaderContainer := environment.Container{
		Name:         "uploader",
		Image:        winUploaderImage,
		ImageIsConst: true,
		Res: environment.Resources{
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

	containers := []*environment.Container{&browserContainer, &recorderContainer, &uploaderContainer}

	env := environment.ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]environment.Volume{
			logVolume: {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
		},
		Network: &environment.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*environment.Endpoint{
				"driver":      {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"healthcheck": {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
			},
		},
		CapacityProvider: config.Conf.AwsWinCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	err := environment.CalculateResources(&env,
		&environment.ResourceCalculationHelper{
			MinimumRes: environment.Resources{Cpu: 1024, Memory: 1024},
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
