package environment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"

	log "github.com/sirupsen/logrus"
)

func buildBrowser(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
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
		return nil, fmt.Errorf("failed to parse timezone. error=%s", err)
	}

	//TODO: handle resolution and video screen size

	mitmIncluded := caps.Mitm
	mitmCommand := "mitmdump --help || sleep infinity"
	var mitmCpu int64 = 32
	var mitmMemory int64 = 64 // minimal memory to start container

	if mitmIncluded {
		// to generate har we have to enable regular dump.mitm output by -w option and place it before har_dump.py!
		mitmCommand = "mitmdump --scripts /har_dump.py -w /tmp/dump.mitm --set hardump=/tmp/dump.har"

		//TODO: wrap into the functions during adding mitm support for other environments (generic, cypress, redroid etc)
		mitmCpu = 512
		if caps.MitmCpu != 0 {
			if caps.MitmCpu > conf.MaxCpu {
				// limit max cpu usage based on cluster configuration
				caps.MitmCpu = conf.MaxCpu
			}
			mitmCpu = caps.MitmCpu
		} else {
			caps.MitmCpu = mitmCpu
		}

		mitmMemory = 512
		if caps.MitmMemory != 0 {
			if caps.MitmMemory > conf.MaxMemory {
				// limit max memory usage based on cluster configuration
				caps.MitmMemory = conf.MaxMemory
			}
			mitmMemory = caps.MitmMemory
		} else {
			caps.MitmMemory = mitmMemory
		}

		if caps.MitmArgs != "" {
			//append args only if mitm=true
			mitmCommand = mitmCommand + " " + caps.MitmArgs
		}
		// --quiet is a must to run without interactive console
		mitmCommand = mitmCommand + " --quiet"
	}
	mitmContainer := Container{
		Name:       "mitm",
		Image:      mitmImage,
		cpu:        mitmCpu,
		memory:     mitmMemory,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
			"LOG_DIR": logDir,
			"COMMAND": 	mitmCommand,
		},
		Ports: map[string]portMapping{
			"fileserverPort": {fileserverPort, 0},
		},
		Mounts:     []string{logVolume},
		Command: []string{"-c", "/entrypoint.sh"},
		EntryPoint: []string{"/bin/sh"},
	}

        taskLogRedirect := ">>" + logDir + "/task.log 2>&1"
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
			"ENABLE_VNC":    strconv.FormatBool(enableVNC),
			"DNS_SERVERS":   strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES": strings.Join(caps.HostsEntries, " "),
			"TZ":            tz.String(),
		},
		Mounts: []string{shmVolume, logVolume},
		Links:      []string{"mitm"},
		Command:    []string{"-c", "/entrypoint.sh" + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(5),
			Retries:     aws.Int64(3),
			Timeout:     aws.Int64(2),
			StartPeriod: aws.Int64(0),
		},
	}
	browserContainer.SetCpu(caps, 1024, conf.MaxCpu)
	browserContainer.SetMemory(caps, 1024, conf.MaxMemory)

	recorderContainer := Container{
		Name:       "recorder",
		Image:      recorderImage,
		cpu:        recorderCpu,
		memory:     recorderMemory,
		Privileged: false,
		Essential:  false,
		Env: map[string]string{
                        "LOG_DIR": logDir,
			"TASK_LOG": logDir + "/task.log",
                        "LOG_FILE": "session.log",
			"ENABLE_VIDEO": "true",
			"ENABLE_MITM": strconv.FormatBool(mitmIncluded),
			"ENABLE_REALTIME_LOGS": "false",
			"BASIC_AUTH":           "",
		},
		Mounts:      []string{logVolume},
		Links:       []string{"browser"},
		Command:     []string{"-c", "/entrypoint.sh" + ">>" + logDir + "/video.log 2>&1"},
		EntryPoint:  []string{"/bin/sh"},
		HealthCheck: nil,
	}
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
                        "LOG_DIR": logDir,
			"S3_KEY_PATTERN":        fmt.Sprintf("s3://%s/%s/artifacts/test-sessions", conf.S3Bucket, workspace),
			"AWS_ACCESS_KEY_ID":     conf.S3AwsAccessKeyID,
			"AWS_SECRET_ACCESS_KEY": conf.S3AwsSecretAccessKey,
			"AWS_DEFAULT_REGION":    conf.S3Region,
		},
		Mounts:      []string{logVolume},
		HealthCheck: nil,
	}

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&mitmContainer, &browserContainer, &recorderContainer, &uploaderContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume:        {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			shmVolume:        {ContainerPath: shmDir, HostPath: shmDir, ReadOnly: false}, // no way to reuse local task volume due to the reset of permissions on browser container start
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
