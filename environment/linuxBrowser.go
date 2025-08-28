package environment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"

	log "github.com/sirupsen/logrus"
)

func buildBrowser(workspace string, routerUUID string, image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	log.Trace("caps: ", caps)

	var (
		// for browsers images try to reuse Doensloads to be able to share this content via upload/download endpoint
		logDir    = "/home/selenium/Downloads"
		logVolume = "log"

		shmDir    = "/dev/shm"
		shmVolume = "shm"

		seleniumDir           = "/home/selenium"
		seleniumBrowserVolume = "seleniumBrowserVolume"
		seleniumMitmVolume    = "seleniumMitmVolume"

		tmpDir            = "/tmp"
		tmpBrowserVolume  = "tmpBrowserVolume"
		tmpRecorderVolume = "tmpRecorderVolume"
		tmpMitmVolume     = "tmpMitmVolume"

		mitmCacheDir    = "/root/.cache"
		mitmCacheVolume = "mitmCacheVolume"

		mitmCertificateDir    = "/root/.mitmproxy"
		mitmCertificateVolume = "mitmCertificateVolume"

		mitmPythonDir    = "/urs/local/lib/python3.11"
		mitmPythonVolume = "mitmPythonVolume"
	)

	tz, err := caps.GetTimeZone()
	// Video recorder & artifacts uploader logic
	if err != nil {
		log.WithError(err).Error("failed to parse timezone")
		return nil, err
	}

	resolution, err := caps.GetScreenResolution()
	if err != nil {
		log.WithError(err).Error("failed to parse screenResolution")
		return nil, err
	}

	taskLogRedirect := ">>" + logDir + "/task.log 2>&1"

	// firefox: --log <LEVEL>                 Set Gecko log level [possible values: fatal, error, warn, info, config, debug, trace]
	// chrome --log-level=LEVEL               set log level: ALL, DEBUG, INFO, WARNING, SEVERE, OFF
	driverArgs := "--log-level=INFO" // Chrome and MicrosoftEdge case to define log level
	if caps.BrowserName == "firefox" {
		// geckodriver case to define log level
		driverArgs = ", \"--log=info\""
	}
	browserContainer := Container{
		Name:      "browser",
		image:     &image,
		Essential: true,
		Ports: map[string]portMapping{
			"driver":         {ContainerPort: seleniumPort, HostPort: 0},
			"vnc":            {ContainerPort: vncPort, HostPort: 0},
			"devtools":       {ContainerPort: devtoolsPort, HostPort: 0},
			"fileserverPort": {ContainerPort: fileserverPort, HostPort: 0},
			"clipboardPort":  {ContainerPort: clipboardPort, HostPort: 0},
		},
		Env: map[string]string{
			"DRIVER_ARGS":       driverArgs,
			"ENABLE_VNC":        strconv.FormatBool(caps.EnableVNC.ToPrimitive()),
			"DNS_SERVERS":       strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES":     strings.Join(caps.HostsEntries, " "),
			"TZ":                tz.String(),
			"SCREEN_RESOLUTION": resolution,
		},
		Mounts:     []string{logVolume, shmVolume, seleniumBrowserVolume, tmpBrowserVolume},
		Command:    []string{"-c", "/entrypoint.sh" + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String(fmt.Sprintf("curl -f localhost:%v/status || exit 1", seleniumPort))},
			Interval:    aws.Int64(8),
			Retries:     aws.Int64(8),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(10),
		},

		ReadOnlyRootFileSystem: true,
	}

	recorderContainer := Container{
		Name:  "recorder",
		Image: recorderImage,
		Res: Resources{
			Cpu:    160, // was 320
			Memory: 1024,
		},
		Privileged: false,
		Essential:  false,
		Ports: map[string]portMapping{
			"recorder": {recorderdPort, 0},
		},
		Env: map[string]string{
			"ROUTER_UUID":          routerUUID,
			"LOG_DIR":              logDir,
			"TASK_LOG":             logDir + "/task.log",
			"LOG_LEVEL":            config.Conf.RecorderLogLvl,
			"LOG_FILE":             "session.log",
			"ENABLE_VIDEO":         strconv.FormatBool(caps.EnableVideo.ToPrimitive()),
			"ENABLE_REALTIME_LOGS": "false",
			"BASIC_AUTH":           "",
			// "CODEC":                caps.VideoCodec.ToPrimitive(), // temporary disabled
		},
		Mounts: []string{logVolume, tmpRecorderVolume},
		Links:  []string{"browser"},
		HealthCheck: &ecs.HealthCheck{
			// check if recorder's binary process is running, no curl is downloaded inside of container
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("pgrep recorder")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(2),
		},
		DependsOn: []*ecs.ContainerDependency{
			{
				ContainerName: aws.String("browser"),
				Condition:     aws.String("START"),
			},
		},

		ReadOnlyRootFileSystem: true,
	}

	if caps.EnableVideo.ToPrimitive() {
		videoSize, err := caps.GetVideoScreenSize(resolution)
		if err != nil {
			log.WithError(err).Error("failed to parse videoScreenSize")
			return nil, err
		}
		recorderContainer.Env["VIDEO_SIZE"] = videoSize

		frameRate, err := caps.GetFrameRate()
		if err != nil {
			log.WithError(err).Error("failed to parse frameRate")
			return nil, err
		}
		recorderContainer.Env["FRAME_RATE"] = frameRate
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
			Cpu:    128, // with 32 uploading is aborted
			Memory: 256, // 64 works for single thread. for backgroud copying it is not enough
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
		Mounts:      []string{logVolume},
		HealthCheck: nil,

		ReadOnlyRootFileSystem: true,
	}

	containers := []*Container{&browserContainer, &recorderContainer, &uploaderContainer}

	var mitmContainer Container
	if caps.Mitm {
		mitmContainer = Container{
			Name:       "mitm",
			Image:      mitmImage,
			Privileged: false,
			Essential:  false,
			Env: map[string]string{
				"LOG_DIR":    logDir,
				"PROXY_ARGS": caps.MitmArgs.ToPrimitive(),
			},
			Ports: map[string]portMapping{
				"fileserverPort":   {ContainerPort: fileserverPort, HostPort: 0},
				"proxyHandlerPort": {ContainerPort: proxyHandlerPort, HostPort: 0},
			},
			Mounts:     []string{logVolume, seleniumMitmVolume, tmpMitmVolume, mitmCacheVolume, mitmCertificateVolume, mitmPythonVolume},
			Command:    []string{"-c", "/entrypoint.sh"},
			EntryPoint: []string{"/bin/sh"},

			ReadOnlyRootFileSystem: true,
		}

		if caps.MitmType.ToPrimitive() != "" {
			mitmContainer.Env["PROXY_TYPE"] = caps.MitmType.ToPrimitive()
		}

		containers = append(containers, &mitmContainer)
		browserContainer.Links = []string{"mitm"}
	}

	env := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume:             {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			shmVolume:             {ContainerPath: shmDir, HostPath: shmDir, ReadOnly: false}, // no way to reuse local task volume due to the reset of permissions on browser container start
			seleniumBrowserVolume: {ContainerPath: seleniumDir, Driver: "local", Scope: "task", ReadOnly: false},
			tmpBrowserVolume:      {ContainerPath: tmpDir, Driver: "local", Scope: "task", ReadOnly: false},
			tmpRecorderVolume:     {ContainerPath: tmpDir, Driver: "local", Scope: "task", ReadOnly: false},
		},
		Network: &network.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*network.Endpoint{
				"driver":        {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"vnc":           {ContainerPort: vncPort, HostPort: 0, Path: "/"},
				"clipboard":     {ContainerPort: clipboardPort, HostPort: 0, Path: "/"},
				"devtools":      {ContainerPort: devtoolsPort, HostPort: 0, Path: "/"},
				"fileserver":    {ContainerPort: fileserverPort, HostPort: 0, Path: "/"},
				"healthcheck":   {ContainerPort: seleniumPort, HostPort: 0, Path: "/"},
				"recorderStart": {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
				"recorderStop":  {ContainerPort: recorderdPort, HostPort: 0, Path: "/stop"},
			},
		},
		Type:             envtype.LINUX,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
		AwsLogsGroup:     config.Conf.AwsLogsGroup,
	}

	if caps.BrowserName == "firefox" {
		env.Network.Endpoints["driver"].Path = "/wd/hub/"
		env.Network.Endpoints["healthcheck"].Path = "/wd/hub/"
	}

	calcArr := make([]*resourceCalculationHelper, 0)
	calcArr = append(calcArr, &resourceCalculationHelper{
		MinimumRes: Resources{Cpu: 1024, Memory: 1024},
		Container:  &browserContainer,
		Memory:     &caps.Memory,
		Cpu:        &caps.Cpu,
	})

	if caps.Mitm {
		env.Network.Endpoints["proxyHandlerPort"] = &network.Endpoint{ContainerPort: proxyHandlerPort, HostPort: 0, Path: "/"}
		calcArr = append(calcArr, &resourceCalculationHelper{
			MinimumRes: Resources{Cpu: 512, Memory: 512},
			Container:  &mitmContainer,
			Memory:     &caps.MitmMemory,
			Cpu:        &caps.MitmCpu,
		})
		env.Volumes[seleniumMitmVolume] = volume{ContainerPath: seleniumDir, Driver: "local", Scope: "task", ReadOnly: false}
		env.Volumes[tmpMitmVolume] = volume{ContainerPath: tmpDir, Driver: "local", Scope: "task", ReadOnly: false}
		env.Volumes[mitmCacheVolume] = volume{ContainerPath: mitmCacheDir, Driver: "local", Scope: "task", ReadOnly: false}
		env.Volumes[mitmCertificateVolume] = volume{ContainerPath: mitmCertificateDir, Driver: "local", Scope: "task", ReadOnly: false}
		env.Volumes[mitmPythonVolume] = volume{ContainerPath: mitmPythonDir, Driver: "local", Scope: "task", ReadOnly: false}
	}

	err = calculateResources(&env, calcArr...)
	if err != nil {
		return nil, err
	}

	return &env, nil
}
