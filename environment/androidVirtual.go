package environment

import (
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/selenium"
)

const (
	appiumPort int64 = 4723

	appiumCpu    = 320
	appiumMemory = 1024
)

func buildAppiumRedroid(workspace string, caps *selenium.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	sharedFolder := "/opt/zebrunner"
	sharedVolume := "data"

	deviceImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	deviceContainer := Container{
		Name:      "device",
		Image:     deviceImage,
                Privileged: true,
		Essential: true,
		Env: map[string]string{
			"VERBOSE": "1",
		},
		Mounts: []string{sharedVolume},
	}
	deviceContainer.SetCpu(caps.Cpu)
	deviceContainer.SetMemory(caps.Memory)
	deviceContainer.SetMemoryReservation(caps.MemoryReservation)

	appiumImage := imageRepo + "appium:1.3-beta15"
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
			"VERBOSE":         "1",
			"RETAIN_TASK":     "false",
			"DEVICE_NAME":     "ReDroid",
			"ANDROID_DEVICES": "device:5555",
                        "REMOTE_ADB":      "true",
			"MCLOUD":          "true",
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.AwsRegion,
		},
		Mounts: []string{sharedVolume},
               Links: []string{"device"},
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&deviceContainer, &appiumContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			sharedVolume: {ContainerPath: sharedFolder, HostPath: sharedFolder, ReadOnly: false},
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver":      {Port: appiumPort, Path: "/wd/hub"},
				"healthcheck": {Port: appiumPort, Path: "/wd/hub/status"},
			},
		},
	}

	return &environment, nil
}
