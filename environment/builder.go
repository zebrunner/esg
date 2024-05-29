package environment

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/images"
)

const (
	uploaderImage        = config.ZebrunnerEcrRegistryUri + "uploader:3.6"
	mitmImage            = config.ZebrunnerEcrRegistryUri + "mitmproxy:2.1"
	recorderImage        = config.ZebrunnerEcrRegistryUri + "recorder:1.5"
	cypressRecorderImage = config.ZebrunnerEcrRegistryUri + "cypress-recorder:1.3"
	appiumImage          = config.ZebrunnerEcrRegistryUri + "appium:2.0.15"
	cloneImage           = config.ZebrunnerEcrRegistryUri + "git:2.36.2"
	entrypointImage      = config.ZebrunnerEcrRegistryUri + "entrypoint:2.5.2"
	mavenImage           = config.ZebrunnerEcrRegistryUri + "m2-repo-carina:1.5"
	winUploaderImage     = config.ZebrunnerEcrRegistryUri + "uploader:1.1-win"
	winRecorderImage     = config.ZebrunnerEcrRegistryUri + "recorder:1.1-win"
)

const (
	seleniumPort     int64 = 4444
	vncPort          int64 = 5900
	devtoolsPort     int64 = 7070
	fileserverPort   int64 = 8080
	clipboardPort    int64 = 9090
	proxyHandlerPort int64 = 8060

	recorderCpu    int64 = 320
	recorderMemory int64 = 1024

	genericPort int64 = 22
	minCpu      int64 = 128
	minMemory   int64 = 256
)

func BuildEnvForTaskDefinitionGeneration(image images.Image, caps *capabilities.Capabilities) (*ExecutionEnvironment, error) {
	buildFn, err := getEnvironmentBuilder(caps)
	if err != nil {
		return nil, err
	}

	return buildFn("", "", image, caps)
}

func BuildEnvForTaskDefinitionOverride(workspace string, caps *capabilities.Capabilities) (*ExecutionEnvironment, string, error) {
	image, err := buildImageFromCaps(caps)
	if err != nil {
		return nil, "", err
	}

	buildFn, err := getEnvironmentBuilder(caps)
	if err != nil {
		return nil, "", err
	}

	routerUUID := uuid.NewString()
	env, err := buildFn(workspace, routerUUID, *image, caps)

	return env, routerUUID, err
}

type envBuilder func(string, string, images.Image, *capabilities.Capabilities) (*ExecutionEnvironment, error)

func getEnvironmentBuilder(caps *capabilities.Capabilities) (envBuilder, error) {
	platform := strings.ToLower(caps.PlatformName.ToPrimitive())

	switch platform {
	case GENERIC.String():
		return buildGeneric, nil
	case LINUX.String():
		return buildBrowser, nil
	case WINDOWS.String():
		return buildWindowsBrowser, nil
	case CYPRESS.String():
		return buildCypress, nil
	case ANDROID.String():
		return buildAppiumRedroid, nil
	case ANY.String():
		return buildBrowser, nil
	default:
		return nil, fmt.Errorf("platform is not supported. platformName=%s", caps.PlatformName)
	}
}

func buildSchema(containers []*Container) string {
	namesArr := make([]string, 0)
	for _, container := range containers {
		namesArr = append(namesArr, container.Name)
	}
	sort.Strings(namesArr)

	return strings.Join(namesArr, "-")
}

func buildImageFromCaps(caps *capabilities.Capabilities) (*images.Image, error) {
	switch caps.PlatformName.ToPrimitive() {
	case GENERIC.String():
		return images.GetGenericImage(caps.Image.ToPrimitive())
	case LINUX.String():
		tag := caps.BrowserVersion.ToPrimitive()
		repository, err := images.RepositoryFromString(caps.BrowserName.ToPrimitive())
		return &images.Image{Repository: repository, Tag: tag}, err
	case WINDOWS.String():
		tag := caps.BrowserVersion.ToPrimitive()
		repository, err := images.RepositoryFromString(fmt.Sprintf("windows-%s", caps.BrowserName.ToPrimitive()))
		return &images.Image{Repository: repository, Tag: tag}, err
	case CYPRESS.String():
		tag := caps.BrowserVersion.ToPrimitive()
		repository, err := images.RepositoryFromString(fmt.Sprintf("windows-%s", caps.BrowserName.ToPrimitive()))
		return &images.Image{Repository: repository, Tag: tag}, err
	case ANDROID.String():
		tag := caps.PlatformVersion.ToPrimitive()
		repository, err := images.RepositoryFromString(caps.DeviceName.ToPrimitive())
		return &images.Image{Repository: repository, Tag: tag}, err
	default:
		return nil, fmt.Errorf("[latform '%s' is not supported", caps.PlatformName.ToPrimitive())
	}
}

func buildTaskDefinitionFamily(caps *capabilities.Capabilities) string {
	familyParts := []string{}

	if zbrEnv := os.Getenv("ZEBRUNNER_ENV"); zbrEnv != "" {
		familyParts = append(familyParts, zbrEnv)
	}

	platformName := strings.ToLower(caps.PlatformName.ToPrimitive())
	if caps.PlatformName == "" || platformName == ANY.String() {
		platformName = LINUX.String()
	}
	familyParts = append(familyParts, platformName)

	if deviceName := strings.ToLower(caps.DeviceName.ToPrimitive()); deviceName != "" {
		deviceName := strings.ToLower(deviceName)
		platformVersion := remapVersion(caps.PlatformVersion.ToPrimitive())

		familyParts = append(familyParts, deviceName, platformVersion)
	} else if browserName := caps.BrowserName.ToPrimitive(); browserName != "" {
		browserName = remapName(browserName)
		browserVersion := remapVersion(caps.BrowserVersion.ToPrimitive())

		familyParts = append(familyParts, browserName, browserVersion)
	}

	return strings.Join(familyParts, "-")
}

func remapName(name string) string {
	name = strings.ToLower(name)

	remapName := map[string]string{
		"microsoftedge": "edge",
	}
	if newName, ok := remapName[name]; ok {
		return newName
	}

	return name
}

func remapVersion(version string) string {
	version = strings.ToLower(version)

	remapVersion := map[string]string{
		"":     "latest",
		"null": "latest",
	}

	if newVersion, ok := remapVersion[version]; ok {
		return newVersion
	}

	version = strings.Replace(version, ".", "-", -1)

	return version
}
