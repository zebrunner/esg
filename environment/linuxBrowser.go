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

        logDir := "/tmp/log"
        logVolume := "log"

	tz, err := caps.GetTimeZone()
        // Video recorder & artifacts uploader logic
        if err != nil {
                return nil, fmt.Errorf("failed to parse timezone. error=%s", err)
        }

	//TODO: handle resolution and video screen size

	sessionLogRedirect :=  " >> " + logDir + "/session.log 2>&1"

        includeMitm := caps.Mitm
	mitmCommand := "mitmdump --help || sleep infinity"
	var mitmCpu int64 = 32
	var mitmMemory int64 = 64 // minimal memory to start container

        if includeMitm {
		// --quiet is a must to run without interactive console
		//to generate har we have to enable regular dump.mitm output by -w option and place it before har_dump.py!
                mitmCommand = "mitmdump --scripts /har_dump.py -w " + logDir + "/dump.mitm --set hardump=" + logDir + "/dump.har"
		mitmCpu = 256
		mitmMemory = 256
		if caps.MitmArgs != "" {
			//append args only if mitm=true
			mitmCommand = mitmCommand + " " + caps.MitmArgs
		}
		mitmCommand = mitmCommand + " --quiet " + sessionLogRedirect

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
                Ports: map[string]portMapping{
                        "fileserverPort": {fileserverPort, 0},
                },
                Mounts:     []string{logVolume},
		//TODO: play with actual command to guarantee mitm.har generation
                Command: []string{"-c", "echo -n >" + logDir + "/session.log && chmod -R a+rwx " + logDir + " && " + mitmCommand}, //IMPORTANT: chmod a+rwx is needed to provide permissions for linked browser into logDir //TODO: how about chown to selenoid (4096:0)
                EntryPoint: []string{"/bin/bash"},
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
		Mounts: []string{"shm", logVolume},
		Links:  []string{"mitm"},
                Command: []string{"-c", "/entrypoint.sh" +  sessionLogRedirect},
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
                                ContainerName: aws.String("mitm"),
                                Condition:  aws.String("START"),
                        },
                },

	}
	browserContainer.SetCpu(caps, 1024, conf.MaxCpu)
	browserContainer.SetMemory(caps, 1024, conf.MaxMemory)

	recorderImage := imageRepo + "video-recorder:1.0"
	videoRecorderContainer := Container{
		Name:              "video-recorder",
		Image:             recorderImage,
		cpu:               recorderCpu,
		memory:            recorderMemory,
		Privileged:        false,
		Essential:         false,
		Mounts:      []string{logVolume},
		Links:       []string{"browser"},
                Command: []string{"-c", "/entrypoint.sh" + sessionLogRedirect},
                EntryPoint: []string{"/bin/sh"},
		HealthCheck: nil,
                DependsOn: []*ecs.ContainerDependency{
                        &ecs.ContainerDependency{
                                ContainerName: aws.String("browser"),
                                Condition:  aws.String("START"),
                        },
                },
	}

        uploaderImage := imageRepo + "artifacts-uploader:2.2"
        uploaderContainer := Container{
                Name:              "artifacts-uploader",
                Image:             uploaderImage,
                cpu:               64, // with 32  uploading is aborted
                memory:            64,
                Privileged:        false,
                Essential:         false,
                Env: map[string]string{
                        "BUCKET":                 conf.S3Bucket,
                        "TENANT":                 workspace,
                        "AWS_ACCESS_KEY_ID":      conf.S3AwsAccessKeyID,
                        "AWS_SECRET_ACCESS_KEY":  conf.S3AwsSecretAccessKey,
                        "AWS_DEFAULT_REGION":     conf.S3Region,
                },
                Mounts:      []string{logVolume},
                HealthCheck: nil,
        }


	environment := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Containers:           []*Container{&browserContainer, &videoRecorderContainer, &mitmContainer, &uploaderContainer},
		Capabilities:         caps,
		Volumes: map[string]volume{
                        logVolume: {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
                        "shm": {ContainerPath: "/dev/shm", HostPath: "/dev/shm", ReadOnly: false},
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
