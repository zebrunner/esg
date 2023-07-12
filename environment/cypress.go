package environment

import (
	"os"

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

	cypressDir := "/tmp/cypress"
	cypressVolume := "cypress"

	entrypointDir := "/opt/entrypoint"
	entrypointVolume := "entrypoint"

	// Potentially it is uselsess based on this article: https://github.com/cypress-io/cypress/pull/9242
	// or one more issue in cypress which in spite of the disabling continue to use it.
	shmDir := "/dev/shm"
	shmVolume := "shm"

	branchArg := ""
	if caps.Branch != "" {
		branchArg = "--branch=" + caps.Branch
	}

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	cloneCommand := "CHANGE_ME"
	taskLogRedirect := ">>" + logDir + "/task.log 2>&1"
	if caps.RepositoryUrl != "" {
		cloneCommand = fmt.Sprintf("git clone --progress --depth=1 --single-branch %s %s %s", branchArg, caps.RepositoryUrl, workDir)
	}

	cloneContainer := Container{
		Name:       "clone",
		Image:      cloneImage,
		cpu:        minCpu,
		memory:     512, //increased memory to fix OOM for huge repositories (3K+ branches)
		Privileged: false,
		Essential:  false,
		Mounts:     []string{taskVolume, logVolume},
		Command:    []string{"-c", cloneCommand + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
	}

	launchCommand := "CHANGE_ME"
	if caps.LaunchCommand != "" {
		launchCommand = caps.LaunchCommand
	}

	entrypointContainer := Container{
		Name:       "entrypoint",
		Image:      entrypointImage,
		cpu:        16,
		memory:     16,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"LOG_DIR":     logDir,
			"WORK_DIR":    workDir,
			"CYPRESS_DIR": cypressDir,
		},
		Mounts:     []string{entrypointVolume, taskVolume, logVolume, cypressVolume},
		EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
		DependsOn: []*ecs.ContainerDependency{
			{
				ContainerName: aws.String("clone"),
				Condition:     aws.String("SUCCESS"),
			},
		},
	}

	// declare hardcoded vars without ability to override:
	entrypointContainer.Env["CYPRESS"] = "true"
	//TODO: move into func
	if strings.EqualFold(conf.LogLevel, "debug") || strings.EqualFold(conf.LogLevel, "trace") {
		entrypointContainer.Env["DEBUG"] = "true"
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
		Mounts:           []string{entrypointVolume, taskVolume, logVolume, cypressVolume, shmVolume},
		WorkingDirectory: workDir,
		Command:          []string{"-c", entrypointDir + "/entrypoint.sh" + taskLogRedirect},
		EntryPoint:       []string{"/bin/sh"},
		HealthCheck: &ecs.HealthCheck{
			//TODO: think about smarter healthcheck
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("exit 0")}, // Healthy as only entrypoint started to init network endpoints ip correctly
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(3),
			Timeout:     aws.Int64(10),
			StartPeriod: aws.Int64(0),
		},
		DependsOn: []*ecs.ContainerDependency{
			{
				ContainerName: aws.String("clone"),
				Condition:     aws.String("SUCCESS"),
			},
			{
				ContainerName: aws.String("entrypoint"),
				Condition:     aws.String("SUCCESS"),
			},
		},
	}

	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			cypressContainer.Env[v] = k
		}
	}
	// declare hardcoded vars without ability to override:
	cypressContainer.Env["CYPRESS"] = "true"
	cypressContainer.Env["CYPRESS_VIDEO"] = "false"

	//basic auth header for executor-logs service
	basicAuthHeader := "Authorization: Basic " + b64.StdEncoding.EncodeToString([]byte(conf.ZebrunnerIntegrationUser+":"+conf.ZebrunnerIntegrationPassword))

	cypressContainer.SetCpu(&caps.Cpu, 1024, conf.MaxCpu)
	cypressContainer.SetMemory(&caps.Memory, 2048, conf.MaxMemory) // 2Gb RAM is minimal for cypress due to the potential memory leaks

	recorderContainer := Container{
		Name:       "recorder",
		Image:      cypressRecorderImage,
		cpu:        recorderCpu,
		memory:     2048,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"ENABLE_VIDEO":         "true",
			"ENABLE_REALTIME_LOGS": "true",
			"BASIC_AUTH":           basicAuthHeader,
			"LOG_FILE":             "session.log",
		},
		Mounts:      []string{logVolume},
		Links:       []string{"browser"},
		Command:     []string{"-c", "/entrypoint.sh" + ">>" + logDir + "/video.log 2>&1"},
		EntryPoint:  []string{"/bin/sh"},
		HealthCheck: nil,
		DependsOn: []*ecs.ContainerDependency{
			{
				ContainerName: aws.String("clone"),
				Condition:     aws.String("SUCCESS"),
			},
			{
				ContainerName: aws.String("entrypoint"),
				Condition:     aws.String("SUCCESS"),
			},
			{
				ContainerName: aws.String("browser"),
				Condition:     aws.String("START"),
			},
		},
	}

	//TODO: do we need sharing vars? it is required for the real time logs only (?!)
	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			recorderContainer.Env[v] = k
		}
	}

	uploaderContainer := Container{
		Name:       "uploader",
		Image:      uploaderImage,
		cpu:        64, // with 32  uploading is aborted
		memory:     64,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"S3_KEY_PATTERN":        fmt.Sprintf("s3://%s/%s/artifacts/test-sessions", conf.S3Bucket, workspace),
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
	}

	// convert image "public.ecr.aws/zebrunner/cypress-chrome:107.0" to task definition failiy: "cypress-cypress-chrome-107-0"
	familyDefinition := strings.Replace(browserImage, imageRepo, "", -1)
	familyDefinition = strings.Replace(familyDefinition, ":", "-", -1)
	familyDefinition = strings.Replace(familyDefinition, ".", "-", -1)
	zbrEnv := os.Getenv("ZEBRUNNER_ENV")
	if zbrEnv != "" {
		familyDefinition = zbrEnv + "-" + familyDefinition
	}
	log.Debug("Overidden TaskDefinitionFamily for cypress: " + familyDefinition)

	containers := []*Container{&cloneContainer, &entrypointContainer, &cypressContainer, &recorderContainer, &uploaderContainer}
	environment := ExecutionEnvironment{
		TaskDefinitionFamily: familyDefinition,
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			taskVolume:       {Driver: "local", Scope: "task", ContainerPath: workDir, ReadOnly: false},
			logVolume:        {Driver: "local", Scope: "task", ContainerPath: logDir, ReadOnly: false},
			cypressVolume:    {Driver: "local", Scope: "task", ContainerPath: cypressDir, ReadOnly: false},
			entrypointVolume: {Driver: "local", Scope: "task", ContainerPath: entrypointDir, ReadOnly: false},
			shmVolume:        {ContainerPath: shmDir, HostPath: shmDir, ReadOnly: false}, // no way to reuse local task volume due to the reset of permissions on browser container start
		},
		Network: &NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*Endpoint{
				"vnc": {ContainerPort: vncPort, HostPort: 0, Path: "/"},
			},
		},
	}

	return &environment, nil
}
