package environment

import (
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	log "github.com/sirupsen/logrus"
)

func buildWindowsBrowser(workspace string, routerUUID string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	caps.EnableVNC.From(false)
	caps.EnableVideo.From(false)
	log.Trace("caps: ", caps)

	tz, err := caps.GetTimeZone()
	if err != nil {
		log.WithError(err).Error("failed to parse timezone")
		return nil, err
	}

	resolution, err := caps.GetScreenResolution()
	if err != nil {
		log.WithError(err).Error("failed to parse screenResolution")
		return nil, err
	}
	browserContainer := Container{
		Name:      "browser",
		Image:     browserImage,
		Essential: true,
		Ports: map[string]portMapping{
			"driver": {seleniumPort, 0},
			// "vnc":            {vncPort, 0},
			// "devtools":       {devtoolsPort, 0},
			// "fileserverPort": {fileserverPort, 0},
			// "clipboardPort":  {clipboardPort, 0},
		},
		Env: map[string]string{
			"ENABLE_VNC":        strconv.FormatBool(caps.EnableVNC.ToPrimitive()),
			"ENABLE_VIDEO":      strconv.FormatBool(caps.EnableVideo.ToPrimitive()),
			"DNS_SERVERS":       strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES":     strings.Join(caps.HostsEntries, " "),
			"TZ":                tz.String(),
			"SCREEN_RESOLUTION": resolution,
		},
		// Mounts:     []string{shmVolume, logVolume},
		// Command:    []string{"-c", "/entrypoint.sh" + taskLogRedirect},
		// EntryPoint: []string{"/bin/sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("cmd.exe"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(0),
		},
	}
	browserContainer.SetCpu(&caps.Cpu, 1024, conf.MaxCpu)
	browserContainer.SetMemory(&caps.Memory, 1024, conf.MaxMemory)

	containers := []*Container{&browserContainer}
	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver": {ContainerPort: seleniumPort, HostPort: 0, Path: "/wd/hub/"},
				// "vnc":         {ContainerPort: vncPort, HostPort: 0, Path: "/"},
				// "clipboard":   {ContainerPort: clipboardPort, HostPort: 0, Path: "/"},
				// "devtools":    {ContainerPort: devtoolsPort, HostPort: 0, Path: "/"},
				// "fileserver":  {ContainerPort: fileserverPort, HostPort: 0, Path: "/"},
				"healthcheck": {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
			},
		},
		Workspace:        workspace,
		RouterUUID:       routerUUID,
		CapacityProvider: config.Conf.AwsWinCP,
	}

	// if caps.BrowserName == "firefox" {
	// 	environment.Network.Endpoints["driver"].Path = "/wd/hub/"
	// 	environment.Network.Endpoints["healthcheck"].Path = "/wd/hub/"
	// }

	return &environment, nil
}
