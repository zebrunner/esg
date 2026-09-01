package environment

import (
	b64 "encoding/base64"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"
	"github.com/zebrunner/esg/utils"
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
		tmpSharedVolume   = "tmpSharedVolume" // Shared between clone and executor

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

		shmDir    = "/dev/shm"
		shmVolume = "shm"
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

	downloadUrls, extractErr := utils.ExtractCapabilityAsString(caps.EnvVariables.ToPrimitive(), "zebrunner:downloadUrls")
	if extractErr != nil {
		log.Debug(extractErr)
	}

	cloneContainer := Container{
		Name:  "clone",
		Image: cloneImage,
		Res: Resources{
			Cpu:    cloneContainerMinCpu,
			Memory: cloneContainerMinMemory,
		},
		Privileged: false,
		Env: map[string]string{
			"CLONE_COMMAND": cloneCommand,
			"DOWNLOAD_URLS": downloadUrls,
		},
		Mounts:                 []string{taskVolume, logVolume, tmpSharedVolume},
		EntryPoint:             []string{"/entrypoint.sh"},
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

	envVars := caps.EnvVariables.ToPrimitive()

	executorProfiles, extractProfilesErr := utils.ExtractCapabilityAsString(envVars, "zebrunner:executorProfiles")
	if extractProfilesErr != nil {
		log.Debug(extractProfilesErr)
	}

	profiles, err := utils.ResolveExecutorProfiles(executorProfiles, envVars["EXECUTOR_PROFILES"], caps.Image.ToPrimitive(), conf.GenericExecutorImageProfiles)
	if err != nil {
		return nil, err
	}

	includeMaven := profiles.Has(utils.ExecutorProfileMaven)
	includePython := profiles.Has(utils.ExecutorProfilePython)
	includeGradle := profiles.Has(utils.ExecutorProfileGradle)
	includePlaywright := profiles.Has(utils.ExecutorProfilePlaywright)

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

	mounts := []string{entrypointVolume, taskVolume, logVolume, tmpSharedVolume, executorCacheVolume, executorConfigVolume, executorNpmVolume}
	if includeMaven {
		mounts = append(mounts, mavenVolume)
		mounts = append(mounts, mavenLogVolume)
	}
	if includePython {
		mounts = append(mounts, executorPythonBinVolume)
		mounts = append(mounts, executorPythonLibVolume)
		mounts = append(mounts, executorPythonLocalVolume)
	}
	if includeGradle {
		mounts = append(mounts, executorGradleVolume)
	}
	if includePlaywright {
		mounts = append(mounts, executorPlaywrightCacheVolume)
		mounts = append(mounts, shmVolume)
	}

	dependsOn := make([]ecsTypes.ContainerDependency, 0)
	if includeMaven {
		dependsOn = append(dependsOn, ecsTypes.ContainerDependency{
			ContainerName: aws.String("maven"),
			Condition:     ecsTypes.ContainerConditionComplete,
		})
	}
	dependsOn = append(dependsOn, ecsTypes.ContainerDependency{
		ContainerName: aws.String("entrypoint"),
		Condition:     ecsTypes.ContainerConditionComplete,
	})
	dependsOn = append(dependsOn, ecsTypes.ContainerDependency{
		ContainerName: aws.String("clone"),
		Condition:     ecsTypes.ContainerConditionComplete,
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
		HealthCheck: &ecsTypes.HealthCheck{
			Command:     []string{"CMD-SHELL", "exit 0"}, // Healthy as container started
			Interval:    aws.Int32(5),
			Retries:     aws.Int32(3),
			Timeout:     aws.Int32(10),
			StartPeriod: aws.Int32(0),
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

	if includePlaywright {
		pwWsEndpoint := ""

		if pwWsEndpoint == "" {
			e3sUrl := strings.ToLower(config.Conf.E3SUrl)
			wsScheme := "ws"
			if strings.HasPrefix(e3sUrl, "https") {
				wsScheme = "wss"
			}
			wsHost := strings.TrimPrefix(strings.TrimPrefix(e3sUrl, "https://"), "http://")
			pwWsEndpoint = fmt.Sprintf("%s://%s/ws/playwright", wsScheme, wsHost)
		}

		executorContainer.Env["PLAYWRIGHT_WS_ENDPOINT"] = pwWsEndpoint
	}

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
		HealthCheck: &ecsTypes.HealthCheck{
			// check if recorder's binary process is running, no curl is downloaded inside of container
			Command:     []string{"CMD-SHELL", "pgrep recorder"},
			Interval:    aws.Int32(5),
			Retries:     aws.Int32(4),
			Timeout:     aws.Int32(5),
			StartPeriod: aws.Int32(2),
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
	volumes[tmpSharedVolume] = volume{Driver: "local", Scope: "task", ContainerPath: tmpDir, ReadOnly: false}
	volumes[executorCacheVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorCacheDir, ReadOnly: false}
	volumes[executorConfigVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorConfigDir, ReadOnly: false}
	volumes[executorNpmVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorNpmDir, ReadOnly: false}

	containers := []*Container{&cloneContainer, &entrypointContainer, &recorderContainer, &uploaderContainer, &executorContainer}
	if includeMaven {
		containers = append(containers, mavenContainer)
		volumes[mavenVolume] = volume{Driver: "local", Scope: "task", ContainerPath: mavenDir, ReadOnly: false}
		volumes[mavenLogVolume] = volume{Driver: "local", Scope: "task", ContainerPath: mavenLogDir, ReadOnly: false}
	}
	if includePython {
		volumes[executorPythonLocalVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPythonLocalDir, ReadOnly: false}
		volumes[executorPythonBinVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPythonBinDir, ReadOnly: false}
		volumes[executorPythonLibVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPythonLibDir, ReadOnly: false}
	}
	if includeGradle {
		volumes[executorGradleVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorGradleDir, ReadOnly: false}
	}
	if includePlaywright {
		volumes[executorPlaywrightCacheVolume] = volume{Driver: "local", Scope: "task", ContainerPath: executorPlaywrighCacheDir, ReadOnly: false}
		volumes[shmVolume] = volume{ContainerPath: shmDir, HostPath: shmDir, ReadOnly: false}
	}

	// Extract custom executor volumes capability directly from ZEBRUNNER_CAPABILITIES env var
	// This bypasses the standard capabilities parsing pipeline (vendor options, pre/post processors)
	// since "zebrunner:executorVolumes" is not part of the standard Capabilities struct
	executorVolumes, extractErr := utils.ExtractCapabilityAsString(caps.EnvVariables.ToPrimitive(), "zebrunner:executorVolumes")

	if extractErr != nil {
		log.Trace(extractErr)
	} else {
		log.Debugf("executorVolumes capability set: %s", executorVolumes)
		executorVolumesPaths := strings.Split(executorVolumes, ",")
		addValidatedVolumes(executorVolumesPaths, "executor-volume", volumes, []*Container{&executorContainer})
	}

	// Extract destination paths from downloadUrls (format: "url1>dest1,url2>dest2")
	// dest includes filename, so extract directory path only for volume mounting
	var downloadDestPaths []string
	if downloadUrls != "" {
		urlPairs := strings.Split(downloadUrls, ",")
		for _, pair := range urlPairs {
			parts := strings.Split(pair, ">")
			if len(parts) == 2 {
				fullPath := strings.TrimSpace(parts[1])
				if fullPath != "" {
					// Extract directory path only (remove filename)
					dirPath := filepath.Dir(fullPath)
					if dirPath != "" && dirPath != "." {
						downloadDestPaths = append(downloadDestPaths, dirPath)
					}
				}
			}
		}
	}

	if len(downloadDestPaths) > 0 {
		addValidatedVolumes(downloadDestPaths, "download-volume", volumes, []*Container{&executorContainer, &cloneContainer})
	}

	var capacityProvider string
	if config.Conf.AwsLinuxGenericCapacityProvider == "" {
		capacityProvider = config.Conf.AwsLinuxCapacityProvider
	} else {
		capacityProvider = config.Conf.AwsLinuxGenericCapacityProvider
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
		CapacityProvider: capacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
		AwsLogsGroup:     config.Conf.AwsLogsGroup,
	}

	err = calculateResources(&env,
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

// addValidatedVolumes validates and adds volume paths to containers and volumes map
// It checks for duplicates, empty paths, and existing paths in volumes
func addValidatedVolumes(paths []string, volumePrefix string, volumes map[string]volume, containers []*Container) {
	seenPaths := make(map[string]bool)

	for i, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		if seenPaths[path] {
			log.Warnf("duplicate %s path skipped (already in request): %s", volumePrefix, path)
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
			log.Warnf("duplicate %s path skipped (already in volumes): %s", volumePrefix, path)
			continue
		}

		seenPaths[path] = true

		mountName := fmt.Sprintf("%s-%d", volumePrefix, i)

		for _, container := range containers {
			container.Mounts = append(container.Mounts, mountName)
		}

		volumes[mountName] = volume{
			Driver:        "local",
			Scope:         "task",
			ContainerPath: path,
			ReadOnly:      false,
		}
	}
}
