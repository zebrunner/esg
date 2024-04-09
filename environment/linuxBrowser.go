package environment

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	log "github.com/sirupsen/logrus"
)

func buildBrowser(workspace string, routerUUID string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	browserImage, err := buildImage(caps)
	if err != nil {
		return nil, err
	}

	log.Trace("caps: ", caps)

	// for browsers images try to reuse Doensloads to be able to share this content via upload/download endpoint
	logDir := "/home/selenium/Downloads"
	logVolume := "log"

	shmDir := "/dev/shm"
	shmVolume := "shm"

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
			"DRIVER_ARGS":       driverArgs,
			"ENABLE_VNC":        strconv.FormatBool(caps.EnableVNC.ToPrimitive()),
			"DNS_SERVERS":       strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES":     strings.Join(caps.HostsEntries, " "),
			"TZ":                tz.String(),
			"SCREEN_RESOLUTION": resolution,
		},
		Mounts:     []string{shmVolume, logVolume},
		Command:    []string{"-c", "/entrypoint.sh" + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(4),
			Timeout:     aws.Int64(5),
			StartPeriod: aws.Int64(0),
		},
	}

	recorderContainer := Container{
		Name:  "recorder",
		Image: recorderImage,
		Res: Resources{
			Cpu:    recorderCpu,
			Memory: recorderMemory,
		},
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"ROUTER_UUID":          routerUUID,
			"LOG_DIR":              logDir,
			"TASK_LOG":             logDir + "/task.log",
			"LOG_FILE":             "session.log",
			"ENABLE_VIDEO":         strconv.FormatBool(caps.EnableVideo.ToPrimitive()),
			"ENABLE_REALTIME_LOGS": "false",
			"BASIC_AUTH":           "",
			// "CODEC":                caps.VideoCodec.ToPrimitive(), // temporary disabled
		},
		Mounts:      []string{logVolume},
		Links:       []string{"browser"},
		Command:     []string{"-c", "/entrypoint.sh" + ">>" + logDir + "/video.log 2>&1"},
		EntryPoint:  []string{"/bin/sh"},
		HealthCheck: nil,
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
			Cpu:    64,  // with 32  uploading is aborted
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
				"fileserverPort":   {fileserverPort, 0},
				"proxyHandlerPort": {proxyHandlerPort, 0},
			},
			Mounts:     []string{logVolume},
			Command:    []string{"-c", "/entrypoint.sh"},
			EntryPoint: []string{"/bin/sh"},
		}

		if caps.MitmType.ToPrimitive() != "" {
			mitmContainer.Env["PROXY_TYPE"] = caps.MitmType.ToPrimitive()
		}

		containers = append(containers, &mitmContainer)
		browserContainer.Links = []string{"mitm"}
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume: {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			shmVolume: {ContainerPath: shmDir, HostPath: shmDir, ReadOnly: false}, // no way to reuse local task volume due to the reset of permissions on browser container start
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
		Workspace:        workspace,
		RouterUUID:       routerUUID,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
	}

	if caps.BrowserName == "firefox" {
		environment.Network.Endpoints["driver"].Path = "/wd/hub/"
		environment.Network.Endpoints["healthcheck"].Path = "/wd/hub/"
	}

	browseMinRes := Resources{Cpu: 1024, Memory: 1024}
	if caps.Mitm {
		// set resources for both mitm and browser containers
		mitmMinRes := Resources{Cpu: 512, Memory: 512}

		calculateResourcesForSeveralContainers(&environment,
			resourceCalculatorHelper{
				MinimumRes: browseMinRes,
				Container:  &browserContainer,
				Memory:     &caps.Memory,
				Cpu:        &caps.Cpu,
			},
			resourceCalculatorHelper{
				MinimumRes: mitmMinRes,
				Container:  &mitmContainer,
				Memory:     &caps.MitmMemory,
				Cpu:        &caps.MitmCpu,
			},
		)

		mitmContainer.CalculateResource(mitmMinRes, environment.CapacityProvider, caps, environment.Containers)
		environment.Network.Endpoints["proxyHandlerPort"] = &Endpoint{ContainerPort: proxyHandlerPort, HostPort: 0, Path: "/"}
	} else {
		browserContainer.CalculateResource(Resources{Cpu: 1024, Memory: 1024}, environment.CapacityProvider, caps, environment.Containers)
	}

	return &environment, nil
}

type resourceCalculatorHelper struct {
	MinimumRes Resources
	Container  *Container
	Memory     capabilities.Wrapper[int64]
	Cpu        capabilities.Wrapper[int64]
	wantedRes  Resources
}

func calculateResourcesForSeveralContainers(env *ExecutionEnvironment, resourcesArr ...resourceCalculatorHelper) {
	freeResource, ok := CapacityProvdirResourcesLimit[env.CapacityProvider]
	if !ok {
		for _, r := range resourcesArr {
			r.Container.Res = r.MinimumRes
		}

		return
	}

	for _, r := range resourcesArr {
		// Clear current container resources setting as it will be configured later
		r.Container.Res = Resources{0, 0}
	}

	busyResources := SumResources(env.Containers)
	freeResource.Remove(&busyResources)

	totalWantedResources := Resources{0, 0}
	for _, r := range resourcesArr {
		wantedCpu := r.Cpu.ToPrimitive() - r.MinimumRes.Cpu
		if wantedCpu < 0 {
			wantedCpu = 0
		}

		wantedMemory := r.Memory.ToPrimitive() - r.MinimumRes.Memory
		if wantedMemory < 0 {
			wantedMemory = 0
		}

		// wanted resources not including minimal values
		r.wantedRes = Resources{Cpu: wantedCpu, Memory: wantedMemory}
		totalWantedResources.Add(&r.wantedRes)

		isCpuOk, isMemoryOk := freeResource.Compare(r.MinimumRes)
		if !isCpuOk || !isMemoryOk {
			r.Container.Res = Resources{-1, -1}
			return
		}

		freeResource.Remove(&r.MinimumRes)
	}

	getExceedCoefficient := func(wantedTotal int64, freeTotal int64) float64 {
		// round up float 2 decimal (1.3333211 -> 1.34)
		exceedsMaximum := math.Ceil((float64(wantedTotal)/float64(freeTotal))*100) / 100
		if exceedsMaximum == 0 {
			exceedsMaximum = 0.01
		}

		return exceedsMaximum
	}

	cpuEnough, memoryEnough := freeResource.Compare(totalWantedResources)
	if !cpuEnough {
		cpuExceedsMaximum := getExceedCoefficient(totalWantedResources.Cpu, freeResource.Cpu)
		for _, r := range resourcesArr {
			// decrease cpu in the same proportion for all conainers
			r.wantedRes.Cpu = int64(float64(r.wantedRes.Cpu) / cpuExceedsMaximum)
		}
	}

	if !memoryEnough {
		memoryExceedsMaximum := getExceedCoefficient(totalWantedResources.Memory, freeResource.Memory)
		for _, r := range resourcesArr {
			// decrease cpu in the same proportion for all conainers
			r.wantedRes.Cpu = int64(float64(r.wantedRes.Cpu) / memoryExceedsMaximum)
		}
	}

	for _, r := range resourcesArr {
		r.Container.Res = Resources{Cpu: r.MinimumRes.Cpu + r.wantedRes.Cpu, Memory: r.MinimumRes.Memory + r.wantedRes.Memory}
		r.Cpu.From(r.Container.Res.Cpu)
		r.Memory.From(r.Container.Res.Memory)
	}
}
