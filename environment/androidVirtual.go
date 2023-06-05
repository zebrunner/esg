package environment

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
)

const (
	appiumPort int64 = 4723

	appiumCpu    = 320
	appiumMemory = 1024
)

func buildAppiumRedroid(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	sharedFolder := "/opt/zebrunner"
	taskVolume := "data"
	browserVolume := "browser"

	conf := &config.Conf

	deviceImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	deviceContainer := Container{
		Name:       "device",
		Image:      deviceImage,
		Privileged: true,
		Essential:  true,
		Env: map[string]string{
			"VERBOSE": "1",
		},
		Mounts: []string{taskVolume},
	}
	deviceContainer.SetCpu(caps.Cpu, 2048, conf.MaxCpu)
	deviceContainer.SetMemory(caps.Memory, 2048, conf.MaxMemory)
	caps.Cpu = deviceContainer.cpu //override default one as we have min/max limits
	caps.Memory = deviceContainer.memory //override default one as we have min/max limits

	appiumContainer := Container{
		Name:       "appium",
		Image:      appiumImage,
		cpu:        appiumCpu,
		memory:     appiumMemory,
		Privileged: false,
		Essential:  true,
		Ports: map[string]portMapping{
			"driver": {appiumPort, 0},
		},
		Env: map[string]string{
			"VERBOSE":               "1",
			"RETAIN_TASK":           "false",
			"DEVICE_NAME":           "ReDroid",
			"ANDROID_DEVICES":       "device:5555",
			"REMOTE_ADB":            "true",
			"MCLOUD":                "true",
			"BUCKET":                conf.S3Bucket,
			"TENANT":                workspace,
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts: []string{taskVolume, browserVolume},
		Links:  []string{"device"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("healthcheck")},
			Retries:     aws.Int64(4),
			Interval:    aws.Int64(10),
			StartPeriod: aws.Int64(60),
		},
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&deviceContainer, &appiumContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume:    {ContainerPath: sharedFolder, Driver: "local", Scope: "task", ReadOnly: false},
			browserVolume: {ContainerPath: "/tmp/zebrunner/chrome", HostPath: "/opt/zebrunner/chrome", ReadOnly: false}, //TODO: think about path unification on hos and inside container
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver":      {ContainerPort: appiumPort, HostPort: 0, Path: "/wd/hub"},
				"healthcheck": {ContainerPort: appiumPort, HostPort: 0, Path: "/wd/hub/status-adb"},
			},
		},
	}

	return &environment, nil
}
