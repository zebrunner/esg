package environment

import (
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"
)

const (
	appiumPort int64 = 4723

	appiumCpu    = 320
	appiumMemory = 1024
)

func buildAppiumRedroid(workspace string, routerUUID string, image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	browserVolume := "browser"

	var (
		logDir    = "/tmp/log"
		logVolume = "log"

		tmpDir          = "/tmp"
		tmpAppiumVolume = "tmpAppium"

		androidDir    = "/root/.android"
		androidVolume = "android"

		appiumHomeDir    = "/usr/lib/node_modules/appium"
		appiumHomeVolume = "appiumHome"

		downloadDir    = "/opt/appium-storage/"
		downloadVolume = "downloadVolume"

		deviceDataDir    = "/data"
		deviceDataVolume = "deviceData"
	)

	caps.EnableVNC = false

	conf := &config.Conf

	deviceContainer := Container{
		Name:       "device",
		image:      &image,
		Privileged: true,
		Essential:  true,
		Env: map[string]string{
			"VERBOSE": "1",
		},
		Mounts:                 []string{deviceDataVolume},
		ReadOnlyRootFileSystem: true,
	}

	appiumContainer := Container{
		Name:  "appium",
		Image: appiumImage,
		Res: Resources{
			Cpu:    appiumCpu,
			Memory: appiumMemory,
		},
		Privileged: false,
		Essential:  true,
		Ports: map[string]portMapping{
			"driver": {ContainerPort: appiumPort, HostPort: appiumPort},
		},
		Env: map[string]string{
			"ROUTER_UUID":          routerUUID,
			"RETAIN_TASK":          "false",
			"DEVICE_NAME":          "ReDroid",
			"DEFAULT_CAPABILITIES": "true",
			"ANDROID_DEVICE":       "localhost:5555",
			"LOG_DIR":              logDir,
			"TASK_LOG":             logDir + "/appium.log",
		},
		Mounts: []string{browserVolume, logVolume, tmpAppiumVolume, androidVolume, appiumHomeVolume, downloadVolume},
		HealthCheck: &ecsTypes.HealthCheck{
			Command:     []string{"CMD-SHELL", "healthcheck"},
			Retries:     aws.Int32(10),
			Interval:    aws.Int32(24),
			StartPeriod: aws.Int32(240),
		},
		ReadOnlyRootFileSystem: true,
	}

	uploaderContainer := Container{
		Name:  "uploader",
		Image: uploaderImage,
		Res: Resources{
			Cpu:    128, // with 32 uploading is aborted
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
		Mounts:                 []string{logVolume},
		HealthCheck:            nil,
		ReadOnlyRootFileSystem: true,
	}

	containers := []*Container{&deviceContainer, &appiumContainer, &uploaderContainer}
	env := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume:        {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			browserVolume:    {ContainerPath: "/tmp/zebrunner/chrome", HostPath: "/opt/zebrunner/chrome", ReadOnly: false}, //TODO: think about path unification on host and inside container
			tmpAppiumVolume:  {ContainerPath: tmpDir, Driver: "local", Scope: "task", ReadOnly: false},
			androidVolume:    {ContainerPath: androidDir, Driver: "local", Scope: "task", ReadOnly: false},
			appiumHomeVolume: {ContainerPath: appiumHomeDir, Driver: "local", Scope: "task", ReadOnly: false},
			downloadVolume:   {ContainerPath: downloadDir, Driver: "local", Scope: "task", ReadOnly: false},
			deviceDataVolume: {ContainerPath: deviceDataDir, Driver: "local", Scope: "task", ReadOnly: false},
		},
		Network: &network.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*network.Endpoint{
				"driver":      {ContainerPort: appiumPort, HostPort: appiumPort, Path: "/wd/hub"},
				"healthcheck": {ContainerPort: appiumPort, HostPort: appiumPort, Path: "/wd/hub/status-adb"},
			},
		},
		Type:             envtype.ANDROID,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
		AwsLogsGroup:     config.Conf.AwsLogsGroup,
	}

	err := calculateResources(&env,
		&resourceCalculationHelper{
			MinimumRes: Resources{Cpu: 2048, Memory: 2048},
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
