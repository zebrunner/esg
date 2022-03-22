package environment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/selenium"
)

const (
	seleniumPort   int64 = 4444
	vncPort        int64 = 5900
	devtoolsPort   int64 = 7070
	fileserverPort int64 = 8080
	clipboardPort  int64 = 9090

	recorderCpu    int64 = 256
	recorderMemory int64 = 768
)

func buildBrowser(workspace string, caps *selenium.Capabilities, conf *config.Config) (*ExecutionEnvironment, error) {
	id := uuid.New().String()

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	// TODO: Find better way to specify this
	sharedFolder := "/opt/zebrunner"
	sharedVolume := "data"

	tz, err := caps.TimeZone()
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
			"ENABLE_VNC":    strconv.FormatBool(caps.EnableVNC),
			"DNS_SERVERS":   strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES": strings.Join(caps.HostsEntries, " "),
			"TZ":            tz.String(),
		},
		Mounts: []string{"shm", sharedVolume},

	}
	browserContainer.SetCpu(caps.Cpu)
	browserContainer.SetMemory(caps.Memory)
	browserContainer.SetMemoryReservation(caps.MemoryReservation)

	// Video recorder & uploader container building logic
	if err != nil {
		return nil, fmt.Errorf("failed to parse timezone. error=%s", err)
	}

	recorderImage := imageRepo + "artifacts-uploader" + ":" + "1.3"
	videoRecorderContainer := Container{
		Name:              "artifacts-uploader",
		Image:             recorderImage,
		cpu:               recorderCpu,
		memory:            recorderMemory,
		memoryReservation: recorderMemory,
		Privileged:        false,
		Essential:         false,
		Env: map[string]string{
			"UUID":                   id,
			"BROWSER_CONTAINER_NAME": "browser",
			"BUCKET":                 conf.S3Bucket,
			"TENANT":                 workspace,
			"AWS_ACCESS_KEY_ID":      conf.AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY":  conf.AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":     conf.AwsRegion,
		},
		Mounts: []string{sharedVolume},
                Links: []string{"browser"},
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&browserContainer, &videoRecorderContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			"shm":        {ContainerPath: "/dev/shm", HostPath: "/dev/shm", ReadOnly: false},
			sharedVolume: {ContainerPath: sharedFolder, HostPath: sharedFolder, ReadOnly: false},
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver":      {Port: seleniumPort, Path: "/"},
				"vnc":         {Port: vncPort, Path: "/"},
				"clipboard":   {Port: clipboardPort, Path: "/"},
				"devtools":    {Port: devtoolsPort, Path: "/"},
				"fileserver":  {Port: fileserverPort, Path: "/"},
				"healthcheck": {Port: seleniumPort, Path: "/"},
			},
		},
	}

	if caps.BrowserName == "firefox" {
		environment.Network.Endpoints["driver"].Path = "/wd/hub"
		environment.Network.Endpoints["healthcheck"].Path = "/wd/hub"
	}

	return &environment, nil
}
