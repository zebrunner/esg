package environment

import (
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/images"
)

const (
	uploaderImage        = config.ZebrunnerEcrRegistryUri + "/" + "uploader:3.6.1"
	mitmImage            = config.ZebrunnerEcrRegistryUri + "/" + "mitmproxy:2.3"
	recorderImage        = config.ZebrunnerEcrRegistryUri + "/" + "recorder:2.3"
	cypressRecorderImage = config.ZebrunnerEcrRegistryUri + "/" + "cypress-recorder:1.3"
	appiumImage          = config.ZebrunnerEcrRegistryUri + "/" + "appium:2.0.15-readonlyfs"
	cloneImage           = config.ZebrunnerEcrRegistryUri + "/" + "git:2.36.2"
	entrypointImage      = config.ZebrunnerEcrRegistryUri + "/" + "entrypoint:2.5.3"
	mavenImage           = config.ZebrunnerEcrRegistryUri + "/" + "m2-repo-carina:1.5"
	winUploaderImage     = config.ZebrunnerEcrRegistryUri + "/" + "uploader:1.1-win"
	winRecorderImage     = config.ZebrunnerEcrRegistryUri + "/" + "recorder:2.0-win"
)

const (
	genericPort      int64 = 22
	seleniumPort     int64 = 4444
	vncPort          int64 = 5900
	devtoolsPort     int64 = 7070
	fileserverPort   int64 = 8080
	clipboardPort    int64 = 9090
	proxyHandlerPort int64 = 8060
	recorderdPort    int64 = 9080

	cypressDebugPort int64 = 9222

	cloneContainerMinCpu    int64 = 128
	cloneContainerMinMemory int64 = 512 //increased memory to fix OOM for huge repositories (3K+ branches)
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
	if err != nil {
		return nil, "", err
	}

	return env, routerUUID, nil
}

type envBuilder func(string, string, images.Image, *capabilities.Capabilities) (*ExecutionEnvironment, error)

func getEnvironmentBuilder(caps *capabilities.Capabilities) (envBuilder, error) {
	platform := strings.ToLower(caps.PlatformName.ToPrimitive())

	switch platform {
	case "":
		fallthrough
	case envtype.ANY.String():
		fallthrough
	case envtype.LINUX.String():
		return buildBrowser, nil
	case envtype.GENERIC.String():
		return buildGeneric, nil
	case envtype.WINDOWS.String():
		return buildWindowsBrowser, nil
	case envtype.CYPRESS.String():
		return buildCypress, nil
	case envtype.ANDROID.String():
		if strings.ToLower(caps.DeviceName.ToPrimitive()) == "redroid" {
			return buildAppiumRedroid, nil
		}
		return nil, fmt.Errorf("device is not supported. deviceName=%s", caps.DeviceName.ToPrimitive())
	default:
		return nil, fmt.Errorf("platform is not supported. platformName=%s", caps.PlatformName.ToPrimitive())
	}
}

func buildImageFromCaps(caps *capabilities.Capabilities) (*images.Image, error) {
	platform := strings.ToLower(caps.PlatformName.ToPrimitive())

	switch platform {
	case "":
		fallthrough
	case envtype.ANY.String():
		fallthrough
	case envtype.LINUX.String():
		return images.ImageFromString(remapName(caps.BrowserName.ToPrimitive()), remapVersion(caps.BrowserVersion.ToPrimitive()))
	case envtype.GENERIC.String():
		return images.GetGenericImage(caps.Image.ToPrimitive())
	case envtype.WINDOWS.String():
		return images.ImageFromString(fmt.Sprintf("windows-%s", remapName(caps.BrowserName.ToPrimitive())), remapVersion(caps.BrowserVersion.ToPrimitive()))
	case envtype.CYPRESS.String():
		// TODO: cyserver should make selenium alike session creation requests
		// delete caps.Image parsing
		// and leave only // return images.ImageFromString(fmt.Sprintf("cypress-%s", caps.BrowserName.ToPrimitive()), caps.BrowserVersion.ToPrimitive())
		if caps.Image.ToPrimitive() == "" {
			return nil, fmt.Errorf("empty image for cypress platform")
		}

		imgArr := strings.Split(caps.Image.ToPrimitive(), "/")
		if len(imgArr) == 0 {
			return nil, fmt.Errorf("invalid image for cypress platform: '%s'", caps.Image.ToPrimitive())
		}

		repositoryTag := imgArr[len(imgArr)-1]
		repositoryTagArr := strings.Split(repositoryTag, ":")
		if len(repositoryTagArr) != 2 || repositoryTagArr[0] == "" || repositoryTagArr[1] == "" {
			return nil, fmt.Errorf("invalid image for cypress platform: '%s'", caps.Image.ToPrimitive())
		}

		repository, tag := repositoryTagArr[0], repositoryTagArr[1]
		caps.BrowserName.From(strings.TrimPrefix(repository, "cypress-"))
		caps.BrowserVersion.From(tag)

		return images.ImageFromString(repository, tag)
	case envtype.ANDROID.String():
		return images.ImageFromString(remapName(caps.DeviceName.ToPrimitive()), remapVersion(caps.PlatformVersion.ToPrimitive()))
	default:
		return nil, fmt.Errorf("platform '%s' is not supported", caps.PlatformName.ToPrimitive())
	}
}

func buildTaskDefinitionFamily(caps *capabilities.Capabilities) string {
	familyParts := []string{}

	if zbrEnv := os.Getenv("ZEBRUNNER_ENV"); zbrEnv != "" {
		familyParts = append(familyParts, zbrEnv)
	}

	platformName := strings.ToLower(caps.PlatformName.ToPrimitive())
	if platformName == "" || platformName == envtype.ANY.String() {
		platformName = envtype.LINUX.String()
	}
	familyParts = append(familyParts, platformName)

	if deviceName := strings.ToLower(caps.DeviceName.ToPrimitive()); deviceName != "" {
		deviceName := strings.ToLower(deviceName)
		platformVersion := remapVersion(caps.PlatformVersion.ToPrimitive())
		platformVersion = strings.Replace(platformVersion, ".", "-", -1)

		familyParts = append(familyParts, deviceName, platformVersion)
	} else if browserName := caps.BrowserName.ToPrimitive(); browserName != "" {
		browserName = remapName(browserName)
		browserVersion := remapVersion(caps.BrowserVersion.ToPrimitive())
		browserVersion = strings.Replace(browserVersion, ".", "-", -1)

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

	return version
}
