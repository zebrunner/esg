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

func buildAppiumRedroid(workspace string, caps *capabilities.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	sharedFolder := "/opt/zebrunner"
	taskVolume := "data"

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
		TaskMounts: []string{taskVolume},
	}
	deviceContainer.SetCpu(caps.Cpu)
	deviceContainer.SetMemory(caps.Memory)
	deviceContainer.SetMemoryReservation(caps.MemoryReservation)

	appiumImage := imageRepo + "appium:1.4.6-beta9"
	appiumContainer := Container{
		Name:              "appium",
		Image:             appiumImage,
		cpu:               appiumCpu,
		memory:            appiumMemory,
		memoryReservation: appiumMemory,
		Privileged:        false,
		Essential:         true,
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
			"AWS_ACCESS_KEY_ID":     conf.AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.AwsRegion,
		},
		TaskMounts: []string{taskVolume},
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
                        taskVolume: {ContainerPath: sharedFolder, Driver: "local", Scope: "task", ReadOnly: false},
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
