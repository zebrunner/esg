package environment

import (
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

	log.Trace("caps: ", caps)

	browserContainer := Container{
		Name:      "browser",
		Image:     browserImage,
		Essential: true,
		Ports: map[string]portMapping{
			"driver": {seleniumPort, 0},
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
				"healthcheck": {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
			},
		},
		Workspace:        workspace,
		RouterUUID:       routerUUID,
		CapacityProvider: config.Conf.AwsWinCapacityProvider,
	}

	return &environment, nil
}
