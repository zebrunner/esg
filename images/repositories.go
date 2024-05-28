package images

import (
	"fmt"
	"strings"

	"github.com/zebrunner/esg/capabilities"
)

const (
	CHROME supportedRepository = iota
	FIREFOX
	EDGE
	REDROID
	WINDOWS_CHROME
	WINDOWS_EDGE
	CYPRESS_CHROME
	CYPRESS_CHROMIUM
	CYPRESS_EDGE
	CYPRESS_FIREFOX
)

type supportedRepository int

func (repository supportedRepository) String() string {
	return [...]string{"chrome", "firefox", "edge", "redroid", "windows-chrome", "windows-edge", "cypress-chrome", "cypress-chromium", "cypress-edge", "cypress-firefox"}[repository]
}

func repositoryFromString(repName string) (supportedRepository, error) {
	switch repName {
	case REDROID.String():
		return REDROID, nil
	case CHROME.String():
		return CHROME, nil
	case FIREFOX.String():
		return FIREFOX, nil
	case EDGE.String():
		return EDGE, nil
	case WINDOWS_CHROME.String():
		return WINDOWS_CHROME, nil
	case WINDOWS_EDGE.String():
		return WINDOWS_EDGE, nil
	case CYPRESS_CHROME.String():
		return CYPRESS_CHROME, nil
	case CYPRESS_CHROMIUM.String():
		return CYPRESS_CHROMIUM, nil
	case CYPRESS_EDGE.String():
		return CYPRESS_EDGE, nil
	case CYPRESS_FIREFOX.String():
		return CYPRESS_FIREFOX, nil
	default:
		return 0, fmt.Errorf("repository with name `%s` is not supported", repName)
	}
}

type capsForPlatform func(executor string, version string) ([]*capabilities.Capabilities, error)

func (repository supportedRepository) getCapsForPlaformFn() capsForPlatform {
	return [...]capsForPlatform{capsForAndroid,
		capsForLinux, capsForLinux, capsForLinux,
		capsForWindows, capsForWindows,
		capsForCypress, capsForCypress, capsForCypress, capsForCypress}[repository]
}

func capsForAndroid(executor string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	caps := capabilities.GetDefaultCaps()
	reqCaps := map[string]interface{}{
		"platformName":    "android",
		"deviceName":      executor,
		"platformVersion": version,
	}

	err := caps.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	return append(capsList, caps), nil
}

func capsForWindows(executor string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)

	reqCaps := map[string]interface{}{
		"platformName":   "windows",
		"browserName":    strings.TrimPrefix(executor, "windows-"),
		"browserVersion": version,
	}
	capsWithoutMitm := capabilities.GetDefaultCaps()
	err := capsWithoutMitm.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	capsList = append(capsList, capsWithoutMitm)
	return capsList, nil
}

func capsForLinux(executor string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	reqCaps := map[string]interface{}{
		"platformName":   "linux",
		"browserName":    executor,
		"browserVersion": version,
	}

	capsWithoutMitm := capabilities.GetDefaultCaps()
	err := capsWithoutMitm.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	capsList = append(capsList, capsWithoutMitm)

	reqCaps["mitm"] = true

	capsWithMitm := capabilities.GetDefaultCaps()
	err = capsWithMitm.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	capsList = append(capsList, capsWithMitm)
	return capsList, nil
}

func capsForCypress(executor string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	caps := capabilities.GetDefaultCaps()
	reqCaps := map[string]interface{}{
		"platformName":   "cypress",
		"browserName":    executor,
		"browserVersion": version,
	}

	err := caps.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	return append(capsList, caps), nil
}
