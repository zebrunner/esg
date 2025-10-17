package environment

import (
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/utils"

	b64 "encoding/base64"
	"fmt"
)

func buildGeneric(workspace string, routerUUID string, image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	caps.EnableVNC = false
	caps.EnableVideo = false

	var (
		workDir    = "/tmp/zebrunner"
		taskVolume = "work"

		logDir    = "/tmp/log"
		logVolume = "log"

		entrypointDir    = "/opt/entrypoint"
		entrypointVolume = "entrypoint"

		mavenDir    = "/root/.m2/repository"
		mavenVolume = "maven"

		mavenLogDir    = "/root/.m2"
		mavenLogVolume = "mavenLog"

		tmpDir            = "/tmp"
		tmpRecorderVolume = "tmpRecorderVolume"
		tmpExecutorVolume = "tmpExecutorVolume"

		executorConfigDir    = "/root/.config"
		executorConfigVolume = "executorConfigVolume"

		executorCacheDir    = "/root/.cache"
		executorCacheVolume = "executorCacheVolume"

		executorPythonLocalDir    = "/root/.local"
		executorPythonLocalVolume = "executorPythonLocalVolume"

		executorPythonBinDir    = "/usr/local/bin"
		executorPythonBinVolume = "executorUserBin"

		executorPythonLibDir    = "/usr/local/lib"
		executorPythonLibVolume = "executorUserLib"

		executorGradleDir    = "/home/gradle"
		executorGradleVolume = "executorGradleHome"

		executorNpmDir    = "/root/.npm"
		executorNpmVolume = "executorNpmVolume"

		executorPlaywrighCacheDir     = "/usr/local/share/.cache"
		executorPlaywrightCacheVolume = "executorPlaywrightCacheVolume"
	)

	branch := ""
	branchArg := ""
	if caps.Branch != "" {
		branch = caps.Branch.ToPrimitive()
		branchArg = "--branch=" + caps.Branch.ToPrimitive()
	}

	if caps.RepositoryUrl == "" {
		return nil, fmt.Errorf("executor repository is not specified! RepositoryUrl='%s'", caps.RepositoryUrl)
	}

	cloneCommand := fmt.Sprintf("git clone --progress --depth=1 --single-branch %s %s %s", branchArg, caps.RepositoryUrl, workDir)
	//fmt.Printf("cloneCommand: %s\n", cloneCommand)

	taskLogRedirect := ">>" + logDir + "/task.log 2>&1"

	cloneContainer := Container{
		Name:  "clone",
		Image: cloneImage,
		Res: Resources{
			Cpu:    cloneContainerMinCpu,
			Memory: cloneContainerMinMemory,
		},
		Privileged:             false,
		Essential:              false,
		Mounts:                 []string{taskVolume, logVolume},
		Command:                []string{"-c", cloneCommand + taskLogRedirect},
		EntryPoint:             []string{"/bin/sh"},
		ReadOnlyRootFileSystem: true,
	}

	entrypointContainer := Container{
		Name:  "entrypoint",
		Image: entrypointImage,
		Res: Resources{
			Cpu:    16,
			Memory: 16,
		},
		Privileged:             false,
		Essential:              false,
		Mounts:                 []string{entrypointVolume, logVolume},
		EntryPoint:             []string{entrypointDir + "/entrypoint.sh"},
		ReadOnlyRootFileSystem: true,
	}

	includeMaven := strings.Contains(caps.Image.ToPrimitive(), "maven")

	includePython := false
	if strings.Contains(caps.Image.ToPrimitive(), "python") || strings.Contains(caps.Image.ToPrimitive(), "amancevice/pandas") {
		includePython = true
	}

	includeGradle := strings.Contains(caps.Image.ToPrimitive(), "gradle")
	includePlaywright := strings.Contains(caps.Image.ToPrimitive(), "playwright")

	var mavenContainer *Container = nil
	if includeMaven {
		mavenContainer = &Container{
			Name:  "maven",
			Image: mavenImage,
			Res: Resources{
				Cpu:    16,
				Memory: 16,
			},
			Privileged:             false,
			Essential:              false,
			Mounts:                 []string{mavenVolume},
			ReadOnlyRootFileSystem: true,
		}
	}

	if caps.LaunchCommand == "" {
		return nil, fmt.Errorf("executor container launch command is not specified! LaunchCommand='%s'", caps.LaunchCommand)
	}
	launchCommand := caps.LaunchCommand

	//basic auth header for executor-logs service
	basicAuthHeader := "Basic " + b64.StdEncoding.EncodeToString([]byte(conf.ZebrunnerIntegrationUser+":"+conf.ZebrunnerIntegrationPassword))

	mounts := []string{entrypointVolume, taskVolume, logVolume, tmpExecutorVolume, executorCacheVolume, executorConfigVolume, executorNpmVolume}
	if includeMaven {
		mounts = append(mounts, mavenVolume)
		mounts = append(mounts, mavenLogVolume)
	} else if includePython {
		mounts = append(mounts, executorPythonBinVolume)
		mounts = append(mounts, executorPythonLibVolume)
		mounts = append(mounts, executorPythonLocalVolume)
	} else if includeGradle {
		mounts = append(mounts, executorGradleVolume)
	} else if includePlaywright {
		mounts = append(mounts, executorPlaywrightCacheVolume)
	}

	dependsOn := make([]*ecs.ContainerDependency, 0)
	if includeMaven {
		dependsOn = append(dependsOn, &ecs.ContainerDependency{
			ContainerName: aws.String("maven"),
			Condition:     aws.String("COMPLETE"),
		})
	}
	dependsOn = append(dependsOn, &ecs.ContainerDependency{
		ContainerName: aws.String("entrypoint"),
		Condition:     aws.String("COMPLETE"),
	})
	dependsOn = append(dependsOn, &ecs.ContainerDependency{
		ContainerName: aws.String("clone"),
		Condition:     aws.String("COMPLETE"),
	})
	executorContainer := Container{
		Name:       "executor",
		image:      &image,
		Privileged: false,
		Essential:  true,
		Env: map[string]string{
			"COMMAND": launchCommand.ToPrimitive(),
			"branch":  branch,
		},
		Mounts:           mounts,
		WorkingDirectory: workDir,
		// we can't redirect logs from this place to support SIGTERM detection on trap
		// actual redirection happens inside entrypoint container: https://github.com/zebrunner/entrypoint/issues/51
		Command: []string{entrypointDir + "/entrypoint.sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("exit 0")}, // Healthy as container started
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(3),
			Timeout:     aws.Int64(10),
			StartPeriod: aws.Int64(0),
		},
		DependsOn:              dependsOn,
		ReadOnlyRootFileSystem: true,
	}

	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			executorContainer.Env[v] = k
		}
	}

	executorContainer.Env["UUID"] = routerUUID
	executorContainer.Env["E3S_URL"] = config.Conf.E3SUrl

	recorderContainer := Container{
		Name:  "recorder",
		Image: recorderImage,
		Res: Resources{
			Cpu:    16, // was 32
			Memory: 64, // was 256 // with 128 failed for cyserver "OutOfMemoryError: Container killed due to memory usage"
		},
		Privileged: false,
		Essential:  false,
		Ports: map[string]portMapping{
			"recorder": {recorderdPort, 0},
		},
		Env: map[string]string{
			"ROUTER_UUID":          routerUUID,
			"ENABLE_VIDEO":         "false",
			"ENABLE_REALTIME_LOGS": "true",
			"LOG_LEVEL":            config.Conf.RecorderLogLvl,
			"BASIC_AUTH":           basicAuthHeader,
			"LOG_FILE":             "console.log",
		},
		Mounts: []string{logVolume, tmpRecorderVolume},
		HealthCheck: &ecs.HealthCheck{
			// check if recorder's binary process is running, no curl is downloaded inside of container
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("pgrep recorder")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(2),
		},
		ReadOnlyRootFileSystem: true,
	}
	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			//fmt.Printf("var: %v; %v\n", v, k)
			recorderContainer.Env[v] = k
		}
	}

	uploaderContainer := Container{
		Name:  "uploader",
		Image: uploaderImage,
		Res: Resources{
			Cpu:    128,
			Memory: 256,
		},
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"S3_KEY_PATTERN":        fmt.Sprintf("s3://%s/%s/artifacts/launches", conf.S3Bucket, workspace),
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:                 []string{logVolume},
		HealthCheck:            nil,
		ReadOnlyRootFileSystem: true,
	}

	volumes := make(map[string]volume, 0)
	volumes[entrypointVolume] = volume{Driver: "local", Scope: "task", ContainerPath: entrypointDir, ReadOnly: false}
	volumes[taskVolume] = volume{Driver: "local", Scope: "task", ContainerPath: workDir, ReadOnly: false}
	volumes[logVolume] = volume{Driver: "local", Scope: "task", ContainerPath: logDir, ReadOnly: false}
	volumes[tmpRecorderVolume] = volume{Driver: "local", Scope: "task", ContainerPath: tmpDir, ReadOnly: false}
	volumes[tmpExecutorVolume] = volume{Driver: "local", Scope: "task", ContainerPath: tmpDir, ReadOnly: false}
	volumes[executorCacheVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorCacheDir, ReadOnly: false}
	volumes[executorConfigVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorConfigDir, ReadOnly: false}
	volumes[executorNpmVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorNpmDir, ReadOnly: false}

	containers := []*Container{&cloneContainer, &entrypointContainer, &recorderContainer, &uploaderContainer, &executorContainer}
	if includeMaven {
		containers = append(containers, mavenContainer)
		volumes[mavenVolume] = volume{Driver: "local", Scope: "task", ContainerPath: mavenDir, ReadOnly: false}
		volumes[mavenLogVolume] = volume{Driver: "local", Scope: "task", ContainerPath: mavenLogDir, ReadOnly: false}
	} else if includePython {
		volumes[executorPythonLocalVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPythonLocalDir, ReadOnly: false}
		volumes[executorPythonBinVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPythonBinDir, ReadOnly: false}
		volumes[executorPythonLibVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPythonLibDir, ReadOnly: false}
	} else if includeGradle {
		volumes[executorGradleVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorGradleDir, ReadOnly: false}
	} else if includePlaywright {
		volumes[executorPlaywrightCacheVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPlaywrighCacheDir, ReadOnly: false}
	}

	// Extract custom executor volumes capability directly from ZEBRUNNER_CAPABILITIES env var
	// This bypasses the standard capabilities parsing pipeline (vendor options, pre/post processors)
	// since "zebrunner:executorVolumes" is not part of the standard Capabilities struct
	executorVolumes, extractErr := utils.ExtractCapabilityAsString(caps.EnvVariables.ToPrimitive(), "zebrunner:executorVolumes")

	if extractErr != nil {
		log.Debug(extractErr)
	} else {
		executorVolumes := strings.Split(executorVolumes, ",")
		seenPaths := make(map[string]bool)

		for i, path := range executorVolumes {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}

			if seenPaths[path] {
				log.Warnf("duplicate ExecutorVolume path skipped (already in request): %s", path)
				continue
			}

			alreadyExists := false
			for _, v := range volumes {
				if v.ContainerPath == path {
					alreadyExists = true
					break
				}
			}
			if alreadyExists {
				log.Warnf("duplicate ExecutorVolume path skipped (already in volumes): %s", path)
				continue
			}

			seenPaths[path] = true

			mountName := fmt.Sprintf("executor-volume-%d", i)
			executorContainer.Mounts = append(executorContainer.Mounts, mountName)

			volumes[mountName] = volume{
				Driver:        "local",
				Scope:         "task",
				ContainerPath: path,
				ReadOnly:      false,
			}
		}
	}

	env := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes:              volumes,
		Network: &network.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*network.Endpoint{
				"driver":         {ContainerPort: genericPort, HostPort: 0, Path: "/"},
				"recorderStart":  {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
				"recorderStop":   {ContainerPort: recorderdPort, HostPort: 0, Path: "/stop"},
				"recorderFinish": {ContainerPort: recorderdPort, HostPort: 0, Path: "/finish"},
			},
		},
		Type:             envtype.GENERIC,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
		AwsLogsGroup:     config.Conf.AwsLogsGroup,
	}

	err := calculateResources(&env,
		&resourceCalculationHelper{
			MinimumRes: Resources{Cpu: 1024, Memory: 1024},
			Container:  &executorContainer,
			Memory:     &caps.Memory,
			Cpu:        &caps.Cpu,
		},
	)
	if err != nil {
		return nil, err
	}

	return &env, nil
}
