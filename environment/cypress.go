package environment

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"

	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	"fmt"
	"strings"

	b64 "encoding/base64"
	log "github.com/sirupsen/logrus"
)

func buildCypress(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	workDir := "/tmp/zebrunner"
	taskVolume := "work"

	logDir := "/tmp/log"
	logVolume := "log"

	entrypointDir := "/opt/entrypoint"
	entrypointVolume := "entrypoint"

	cypressDir := "/opt/cypress"
	cypressVolume := "cypress"

	//TODO: test removal of /opt/zebrunner for cypress
	zebrunnerDir := "/opt/zebrunner"
	zebrunnerVolume := "zebrunner"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch
	}

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}
	log.Debug("browserImage: " + browserImage)

	cloneCommand := "CHANGE_ME"
	sessionLogRedirect := ">>" + logDir + "/session.log 2>&1"
	if caps.RepositoryUrl != "" {
		cloneCommand = fmt.Sprintf("git clone --depth=1 --single-branch %s %s %s", branchArg, caps.RepositoryUrl, workDir)
		cloneCommand = cloneCommand + sessionLogRedirect + " ; chown -R 4096:4096 " + workDir + "; chown -R 4096:4096 " + logDir
	}

	cloneImage := imageRepo + "git:latest"
	cloneContainer := Container{
		Name:       "clone",
		Image:      cloneImage,
		cpu:        minCpu,
		memory:     512,
		Privileged: false,
		Essential:  false,
		Mounts:     []string{taskVolume, logVolume},
		Command:    []string{"-c", cloneCommand + sessionLogRedirect},
		EntryPoint: []string{"/bin/sh"},
	}

	launchCommand := "CHANGE_ME"
	if caps.LaunchCommand != "" {
		launchCommand = caps.LaunchCommand
	}

	entrypointImage := imageRepo + "entrypoint:2.0"
	entrypointContainer := Container{
		Name:       "entrypoint",
		Image:      entrypointImage,
		cpu:        minCpu,
		memory:     minMemory,
		Privileged: false,
		Essential:  false,
		Mounts:     []string{entrypointVolume, cypressVolume},
		EntryPoint: []string{entrypointDir + "/entrypoint.sh"}, //TODO: do we need output in session.log?
	}

	cypressContainer := Container{
		Name:       "browser",
		Image:      browserImage,
		Privileged: false,
		Essential:  true,
		Ports: map[string]portMapping{
			"vnc": {vncPort, 0},
		},
		Env: map[string]string{
			"COMMAND": launchCommand,
		},
		Mounts:           []string{entrypointVolume, taskVolume, logVolume, zebrunnerVolume, cypressVolume},
		WorkingDirectory: workDir,
		Command:          []string{"-c", entrypointDir + "/entrypoint.sh" + sessionLogRedirect},
		EntryPoint:       []string{"/bin/sh"},
		DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
				ContainerName: aws.String("entrypoint"),
				Condition:     aws.String("COMPLETE"),
			},
			&ecs.ContainerDependency{
				ContainerName: aws.String("clone"),
				Condition:     aws.String("COMPLETE"),
			},
		},
	}

	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			cypressContainer.Env[v] = k
		}
	}

	cypressContainer.SetCpu(caps, 1024, conf.MaxCpu)
	cypressContainer.SetMemory(caps, 2048, conf.MaxMemory) // 2Gb RAM is minimal for cypress due to the potential memory leaks

	recorderImage := imageRepo + "video-recorder:1.0"
	videoRecorderContainer := Container{
		Name:        "video-recorder",
		Image:       recorderImage,
		cpu:         recorderCpu,
		memory:      recorderMemory,
		Privileged:  false,
		Essential:   false,
		Mounts:      []string{logVolume},
		Links:       []string{"browser"},
		Command:     []string{"-c", "/entrypoint.sh" + sessionLogRedirect},
		EntryPoint:  []string{"/bin/sh"},
		HealthCheck: nil,
		DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
				ContainerName: aws.String("browser"),
				Condition:     aws.String("START"),
			},
		},
	}

	//basic auth header for executor-logs service
	basicAuthHeader := "Authorization: Basic " + b64.StdEncoding.EncodeToString([]byte(conf.ZebrunnerIntegrationUser+":"+conf.ZebrunnerIntegrationPassword))

	uploaderImage := imageRepo + "artifacts-uploader:2.2"
	uploaderContainer := Container{
		Name:       "artifacts-uploader",
		Image:      uploaderImage,
		cpu:        64,
		memory:     64,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"BASIC_AUTH":            basicAuthHeader,
			"BUCKET":                conf.S3Bucket,
			"TENANT":                workspace,
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
	}

	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			uploaderContainer.Env[v] = k
		}
	}

	// convert image "public.ecr.aws/zebrunner/cypress-chrome:107.0" to task definition failiy: "cypress-cypress-chrome-107-0"
	familyDefinition := strings.Replace(browserImage, imageRepo, "", -1)
	familyDefinition = strings.Replace(familyDefinition, ":", "-", -1)
	familyDefinition = strings.Replace(familyDefinition, ".", "-", -1)
	log.Debug("Overidden TaskDefinitionFamily for cypress: " + familyDefinition)

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: familyDefinition,
		Containers:           []*Container{&cloneContainer, &entrypointContainer, &cypressContainer, &videoRecorderContainer, &uploaderContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume:       {Driver: "local", Scope: "task", ContainerPath: workDir, ReadOnly: false},
			logVolume:        {Driver: "local", Scope: "task", ContainerPath: logDir, ReadOnly: false},
			entrypointVolume: {Driver: "local", Scope: "task", ContainerPath: entrypointDir, ReadOnly: false},
			cypressVolume:    {Driver: "local", Scope: "task", ContainerPath: cypressDir, ReadOnly: false},
			zebrunnerVolume:  {HostPath: zebrunnerDir, ContainerPath: zebrunnerDir, ReadOnly: true},
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"driver": {ContainerPort: genericPort, HostPort: 0, Path: "/"},
				"vnc":    {ContainerPort: vncPort, HostPort: 0, Path: "/"},
			},
		},
	}

	return &environment, nil
}
