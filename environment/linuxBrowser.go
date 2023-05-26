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

        entrypointDir := "/opt/entrypoint"
        entrypointVolume := "entrypoint"

	logDir := "/tmp/log"
	logVolume := "log"

	tz, err := caps.GetTimeZone()
	// Video recorder & artifacts uploader logic
	if err != nil {
		return nil, fmt.Errorf("failed to parse timezone. error=%s", err)
	}

	//TODO: handle resolution and video screen size

        taskLogRedirect := ">>" + logDir + "/task.log 2>&1"

/*	includeMitm := caps.Mitm
	mitmCommand := "mitmdump --help || sleep infinity"
	var mitmCpu int64 = 32
	var mitmMemory int64 = 64 // minimal memory to start container

	if includeMitm {
		// --quiet is a must to run without interactive console
		//to generate har we have to enable regular dump.mitm output by -w option and place it before har_dump.py!
    mitmCommand = "mitmdump --scripts /har_dump.py -w " + logDir + "/dump.mitm --set hardump=" + logDir + "/dump.har"
		mitmCpu = 512
		mitmMemory = 512
		if caps.MitmArgs != "" {
			//append args only if mitm=true
			mitmCommand = mitmCommand + " " + caps.MitmArgs
		}
		mitmCommand = mitmCommand + " --quiet "

		//TODO: register such capabilities automatically: -Dproxy_host=mitm -Dproxy_port=8080

	}
  mitmImage := imageRepo + "mitmproxy:1.0"
  mitmContainer := Container{
    Name:       "mitm",
    Image:      mitmImage,
    cpu:        mitmCpu,
    memory:     mitmMemory,
    Privileged: false,
    Essential:  false,
    Env: map[string]string{
      "COMMAND": 	mitmCommand,
    },
    Ports: map[string]portMapping{
      "fileserverPort": {fileserverPort, 0},
    },
    Mounts:     []string{logVolume},
    Command: []string{"-c", "/entrypoint.sh" +  taskLogRedirect},
    EntryPoint: []string{"/bin/sh"},
  }
*/
        entrypointImage := imageRepo + "entrypoint:2.0"
        entrypointContainer := Container{
                Name:       "entrypoint",
                Image:      entrypointImage,
                cpu:        16,
                memory:     16,
                Privileged: false,
                Essential:  false,
                Mounts:     []string{entrypointVolume, logVolume},
                EntryPoint: []string{entrypointDir + "/entrypoint.sh"},
        }

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
		Mounts:     []string{"shm", logVolume},
//		Links:      []string{"mitm"},
		Command:    []string{"-c", "/entrypoint.sh" + taskLogRedirect},
		EntryPoint: []string{"/bin/sh"},
		HealthCheck: &ecs.HealthCheck{
			Command:     []*string{aws.String("CMD-SHELL"), aws.String("curl -f localhost:4444/status || exit 1")},
			Interval:    aws.Int64(10),
			Retries:     aws.Int64(3),
			Timeout:     aws.Int64(10),
			StartPeriod: aws.Int64(5),
		},
                DependsOn: []*ecs.ContainerDependency{
                        &ecs.ContainerDependency{
                                ContainerName: aws.String("entrypoint"),
                                Condition:     aws.String("COMPLETE"),
                        },
                },
	}
	browserContainer.SetCpu(caps, 1024, conf.MaxCpu)
	browserContainer.SetMemory(caps, 1024, conf.MaxMemory)

	recorderImage := imageRepo + "recorder:1.0"
	recorderContainer := Container{
		Name:        "recorder",
		Image:       recorderImage,
		cpu:         recorderCpu,
		memory:      recorderMemory,
		Privileged:  false,
		Essential:   false,
                Env: map[string]string{
                        "ENABLE_VIDEO":          "true",
                        "ENABLE_REALTIME_LOGS":  "false",
                        "BASIC_AUTH":            "",
                        "LOG_FILE":              "session.log",
                },
		Mounts:      []string{logVolume},
		Links:       []string{"browser"},
		Command:     []string{"-c", "/entrypoint.sh" + ">>" + logDir + "/video.log 2>&1"},
		EntryPoint:  []string{"/bin/sh"},
		HealthCheck: nil,
		DependsOn: []*ecs.ContainerDependency{
			&ecs.ContainerDependency{
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

	uploaderImage := imageRepo + "uploader:2.2"
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

	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&entrypointContainer, &browserContainer, &recorderContainer, &uploaderContainer}, //&mitmContainer
		Capabilities:         caps,
		Volumes: map[string]volume{
                        entrypointVolume: {ContainerPath: entrypointDir, Driver: "local", Scope: "task", ReadOnly: false},
			logVolume: {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			"shm":     {ContainerPath: "/dev/shm", HostPath: "/dev/shm", ReadOnly: false},
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
