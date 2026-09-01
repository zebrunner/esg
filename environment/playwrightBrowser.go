package environment

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	ecsTypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/environment/network"
	"github.com/zebrunner/esg/images"
)

const (
	playwrightPort        int64 = 5555
	playwrightControlPort int64 = 5556
	playwrightCdpPort     int64 = 9222
)

// The image ships no branded channels, so chrome and edge resolve to the bundled chromium engine.
// The empty key serves task definition generation only, where no browser is requested yet.
var playwrightBrowserTypes = map[string]string{
	"":              "playwright-chromium",
	"chromium":      "playwright-chromium",
	"chrome":        "playwright-chromium",
	"edge":          "playwright-chromium",
	"microsoftedge": "playwright-chromium",
	"firefox":       "playwright-firefox",
	"webkit":        "playwright-webkit",
	"safari":        "playwright-webkit",
}

// PlaywrightCatalogBrowsers are the engine names GET /browsers advertises for each playwright image.
var PlaywrightCatalogBrowsers = []string{"chromium", "firefox", "webkit"}

// ResolvePlaywrightBrowserType maps a webdriver browser name onto the BROWSER_TYPE value of the image.
func ResolvePlaywrightBrowserType(browserName string) (string, error) {
	name := strings.TrimPrefix(strings.ToLower(browserName), "playwright-")

	browserType, ok := playwrightBrowserTypes[name]
	if !ok {
		return "", fmt.Errorf("browser is not supported on playwright platform. browserName=%s", browserName)
	}

	return browserType, nil
}

func buildPlaywright(workspace string, routerUUID string, image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	conf := &config.Conf

	log.Trace("caps: ", caps)

	var (
		logDir    = "/tmp/log"
		logVolume = "log"

		shmDir    = "/dev/shm"
		shmVolume = "shm"

		tmpDir            = "/tmp"
		tmpBrowserVolume  = "tmpBrowserVolume"
		tmpRecorderVolume = "tmpRecorderVolume"
	)

	tz, err := caps.GetTimeZone()
	if err != nil {
		log.WithError(err).Error("failed to parse timezone")
		return nil, err
	}

	resolution, err := caps.GetScreenResolution()
	if err != nil {
		log.WithError(err).Error("failed to parse screenResolution")
		return nil, err
	}

	resParts := strings.Split(resolution, "x")
	screenWidth := "1920"
	screenHeight := "1080"
	if len(resParts) >= 2 {
		screenWidth = resParts[0]
		screenHeight = resParts[1]
	}

	browserType, err := ResolvePlaywrightBrowserType(caps.BrowserName.ToPrimitive())
	if err != nil {
		log.WithError(err).Error("failed to resolve playwright browser type")
		return nil, err
	}

	browserContainer := Container{
		Name:      "browser",
		image:     &image,
		Essential: true,
		Ports: map[string]portMapping{
			"driver":   {ContainerPort: playwrightPort, HostPort: 0},
			"control":  {ContainerPort: playwrightControlPort, HostPort: 0},
			"vnc":      {ContainerPort: vncPort, HostPort: 0},
			"devtools": {ContainerPort: playwrightCdpPort, HostPort: 0},
		},
		Env: map[string]string{
			"BROWSER_TYPE":        browserType,
			"PLAYWRIGHT_HEADLESS": strconv.FormatBool(caps.Headless.ToPrimitive()),
			"ENABLE_VNC":          strconv.FormatBool(caps.EnableVNC.ToPrimitive()),
			"TZ":                  tz.String(),
			"SE_SCREEN_WIDTH":     screenWidth,
			"SE_SCREEN_HEIGHT":    screenHeight,
			"DNS_SERVERS":         strings.Join(caps.DNSServers, " "),
			"HOSTS_ENTRIES":       strings.Join(caps.HostsEntries, " "),
		},
		Mounts: []string{logVolume, shmVolume, tmpBrowserVolume},
		// The recorder truncates the task log on rotate, so append mode keeps writes at offset 0.
		Command:    []string{"-c", "/opt/bin/entrypoint.sh 2>&1 | tee -a " + logDir + "/task.log"},
		EntryPoint: []string{"/bin/bash"},
		HealthCheck: &ecsTypes.HealthCheck{
			// The supervisor outlives every browser, so a refresh never fails the probe.
			Command:     []string{"CMD-SHELL", fmt.Sprintf("curl -sf http://localhost:%d/health || exit 1", playwrightControlPort)},
			Interval:    aws.Int32(10),
			Timeout:     aws.Int32(5),
			Retries:     aws.Int32(6),
			StartPeriod: aws.Int32(15),
		},

		ReadOnlyRootFileSystem: false,
	}

	if args := caps.PlaywrightArgs.ToPrimitive(); args != "" {
		browserContainer.Env["PLAYWRIGHT_EXTRA_ARGS"] = args
	}

	if caps.EnvVariables != nil {
		for v, k := range caps.EnvVariables {
			browserContainer.Env[v] = k
		}
	}

	recorderContainer := Container{
		Name:  "recorder",
		Image: recorderImage,
		Res: Resources{
			Cpu:    160,
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
		},
		Mounts: []string{logVolume, tmpRecorderVolume},
		Links:  []string{"browser"},
		HealthCheck: &ecsTypes.HealthCheck{
			Command:     []string{"CMD-SHELL", "pgrep recorder"},
			Interval:    aws.Int32(5),
			Retries:     aws.Int32(4),
			Timeout:     aws.Int32(5),
			StartPeriod: aws.Int32(2),
		},
		DependsOn: []ecsTypes.ContainerDependency{
			{
				ContainerName: aws.String("browser"),
				Condition:     ecsTypes.ContainerConditionStart,
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

	env := ExecutionEnvironment{
		TaskDefinitionFamily: buildTaskDefinitionFamily(caps),
		Schema:               buildSchema(containers),
		Containers:           containers,
		Capabilities:         caps,
		Volumes: map[string]volume{
			logVolume:         {ContainerPath: logDir, Driver: "local", Scope: "task", ReadOnly: false},
			shmVolume:         {ContainerPath: shmDir, HostPath: shmDir, ReadOnly: false},
			tmpBrowserVolume:  {ContainerPath: tmpDir, Driver: "local", Scope: "task", ReadOnly: false},
			tmpRecorderVolume: {ContainerPath: tmpDir, Driver: "local", Scope: "task", ReadOnly: false},
		},
		Network: &network.NetworkConfiguration{
			IP: "",
			Endpoints: map[string]*network.Endpoint{
				"driver":            {ContainerPort: playwrightPort, HostPort: 0, Path: "/"},
				"playwrightRefresh": {ContainerPort: playwrightControlPort, HostPort: 0, Path: "/refresh"},
				"playwrightHealth":  {ContainerPort: playwrightControlPort, HostPort: 0, Path: "/health"},
				"vnc":               {ContainerPort: vncPort, HostPort: 0, Path: "/"},
				"devtools":          {ContainerPort: playwrightCdpPort, HostPort: 0, Path: "/"},
				"healthcheck":       {ContainerPort: playwrightCdpPort, HostPort: 0, Path: "/"},
				"recorderStart":     {ContainerPort: recorderdPort, HostPort: 0, Path: "/start"},
				"recorderStop":      {ContainerPort: recorderdPort, HostPort: 0, Path: "/stop"},
				"recorderRotate":    {ContainerPort: recorderdPort, HostPort: 0, Path: "/rotate"},
				"recorderFinish":    {ContainerPort: recorderdPort, HostPort: 0, Path: "/finish"},
			},
		},
		Type:             envtype.PLAYWRIGHT,
		CapacityProvider: config.Conf.AwsLinuxCapacityProvider,
		TaskRoleArn:      config.Conf.AwsTaskRoleArn,
		AwsLogsGroup:     config.Conf.AwsLogsGroup,
	}

	err = calculateResources(&env,
		&resourceCalculationHelper{
			MinimumRes: Resources{Cpu: 1024, Memory: 2048},
			Container:  &browserContainer,
			Memory:     &caps.Memory,
			Cpu:        &caps.Cpu,
		},
	)
	if err != nil {
		return nil, err
	}

	return &env, nil
}
