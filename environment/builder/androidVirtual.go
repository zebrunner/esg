package builder

import (
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"github.com/zebrunner/esg/images"
)

const (
	appiumPort int64 = 4723

	appiumCpu    = 320
	appiumMemory = 1024
)

func buildAppiumRedroid(workspace string, routerUUID string, image *images.Image, caps *capabilities.Capabilities) (*environment.ExecutionEnvironment, error) {
	browserVolume := "browser"

	logDir := "/tmp/log"
	logVolume := "log"

	caps.EnableVNC = false

	conf := &config.Conf

	deviceContainer := environment.Container{
		Name:         "device",
		ImageIsConst: false,
		Privileged:   true,
		Essential:    true,
		Env: map[string]string{
			"VERBOSE": "1",
		},
	}

	if image != nil {
		deviceContainer.Image = image.GetUrl()
	}

	appiumContainer := environment.Container{
		Name:         "appium",
		Image:        appiumImage,
		ImageIsConst: true,
		Res: environment.Resources{
			Cpu:    appiumCpu,
			Memory: appiumMemory,
		},
		Privileged: false,
		Essential:  true,
		Ports: map[string]environment.PortMapping{
			"driver": {ContainerPort: appiumPort, HostPort: 0},
		},
		Env: map[string]string{
			"ROUTER_UUID":    routerUUID,
			"RETAIN_TASK":    "false",
			"DEVICE_NAME":    "ReDroid",
			"ANDROID_DEVICE": "device:5555",
			"LOG_DIR":        logDir,
			"TASK_LOG":       logDir + "/appium.log",
		},
		Mounts: []string{browserVolume, logVolume},
		Links:  []string{"device"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("healthcheck")},
			Retries:     aws.Int64(10),
			Interval:    aws.Int64(24),
			StartPeriod: aws.Int64(240),
		},
	}

	uploaderContainer := environment.Container{
		Name:         "uploader",
		Image:        uploaderImage,
		ImageIsConst: true,
		Res: environment.Resources{
			Cpu:    64,  // with 32  uploading is aborted
			Memory: 256, // 64 works for single thread. for background copying it is not enough
		},
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"LOG_DIR":               logDir,
			"S3_KEY_PATTERN":        fmt.Sprintf("s3://%s/%s/artifacts/test-sessions", conf.S3Bucket, workspace),
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
	}

	containers := []*environment.Container{&deviceContainer, &appiumContainer, &uploaderContainer}
	env := environment.ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]environment.Volume{
			logVolume:     {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			browserVolume: {ContainerPath: "/tmp/zebrunner/chrome", HostPath: "/opt/zebrunner/chrome", ReadOnly: false}, //TODO: think about path unification on host and inside container
		},
		Network: &environment.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*environment.Endpoint{
				"driver":      {ContainerPort: appiumPort, HostPort: 0, Path: "/wd/hub"},
				"healthcheck": {ContainerPort: appiumPort, HostPort: 0, Path: "/wd/hub/status-adb"},
			},
		},
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	err := environment.CalculateResources(&env,
		&environment.ResourceCalculationHelper{
			MinimumRes: environment.Resources{Cpu: 2048, Memory: 2048},
			Container:  &deviceContainer,
			Memory:     &caps.Memory,
			Cpu:        &caps.Cpu,
		},
	)
	if err != nil {
		return nil, err
	}

	return &env, nil
}
