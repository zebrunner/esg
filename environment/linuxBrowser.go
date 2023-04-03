package environment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/google/uuid"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
)

func buildBrowser(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
        conf := &config.Conf

	id := uuid.New().String()

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	// TODO: Find better way to specify this
	sharedFolder := "/opt/zebrunner"
	taskVolume := "data"
        dockerSocketVolume := "docker-socket"


	tz, err := caps.GetTimeZone()
	// In future maybe there will be need to disable vnc
	enableVNC := true
	browserContainer := Container{
		Name:      "browser",
		Image:     browserImage,
		Essential: true,
		Ports: map[string]portMapping{
			"driver":         {seleniumPort, 0},
			"vnc":            {vncPort, 0},
			"devtools":       {devtoolsPort, 0},
			"fileserverPort": {fileserverPort, 0},
			"clipboardPort":  {clipboardPort, 0},
		},
		Env: map[string]string{
			"VERBOSE":       "1",
			"UUID":          id,
			"ENABLE_VNC":    strconv.FormatBool(enableVNC),
			"DNS_SERVERS":   strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES": strings.Join(caps.HostsEntries, " "),
			"TZ":            tz.String(),
		},
		Mounts: []string{"shm", taskVolume},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(10),
			Retries:     aws.Int64(3),
			Timeout:     aws.Int64(10),
			StartPeriod: aws.Int64(5),
		},
	}
	browserContainer.SetCpu(caps, 1024, conf.MaxCpu)
	browserContainer.SetMemory(caps, 1024, conf.MaxMemory)

	// Video recorder & artifacts uploader logic
	if err != nil {
		return nil, fmt.Errorf("failed to parse timezone. error=%s", err)
	}

	recorderImage := imageRepo + "artifacts-uploader:2.1"
	videoRecorderContainer := Container{
		Name:              "artifacts-uploader",
		Image:             recorderImage,
		cpu:               recorderCpu,
		memory:            recorderMemory,
		Privileged:        false,
		Essential:         false,
		Env: map[string]string{
			"UUID":                   id,
			"BROWSER_CONTAINER_NAME": "browser",
			"BUCKET":                 conf.S3Bucket,
			"TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.S3AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.S3AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.S3Region,
		},
		Mounts:      []string{taskVolume, dockerSocketVolume},
		Links:       []string{"browser"},
		HealthCheck: nil,
                DependsOn: []*ecs.ContainerDependency{
                        &ecs.ContainerDependency{
                                ContainerName: aws.String("browser"),
                                Condition:  aws.String("START"),
                        },
                },
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&browserContainer, &videoRecorderContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
                        taskVolume: {ContainerPath: sharedFolder, Driver: "local", Scope: "task", ReadOnly: false},
                        "shm": {ContainerPath: "/dev/shm", HostPath: "/dev/shm", ReadOnly: false},
			dockerSocketVolume: {ContainerPath: "/var/run/docker.sock", HostPath: "/var/run/docker.sock", ReadOnly: false},
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver":      {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"vnc":         {ContainerPort: vncPort, HostPort: 0, Path: "/"},
				"clipboard":   {ContainerPort: clipboardPort, HostPort: 0, Path: "/"},
				"devtools":    {ContainerPort: devtoolsPort, HostPort: 0, Path: "/"},
				"fileserver":  {ContainerPort: fileserverPort, HostPort: 0, Path: "/"},
				"healthcheck": {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
			},
		},
	}

	if caps.BrowserName == "firefox" {
		environment.Network.Endpoints["driver"].Path = "/wd/hub/"
		environment.Network.Endpoints["healthcheck"].Path = "/wd/hub/"
	}

	return &environment, nil
}
