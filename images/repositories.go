package images

import (
	"fmt"

	envtype "github.com/zebrunner/esg/environment/envType"
)

const (
	GENERIC supportedRepository = iota
	CHROME
	FIREFOX
	EDGE
	REDROID
	WINDOWS_CHROME
	WINDOWS_EDGE
	WINDOWS_FIREFOX
	CYPRESS_CHROME
	CYPRESS_CHROMIUM
	CYPRESS_EDGE
	CYPRESS_FIREFOX
)

type supportedRepository int

func (repository supportedRepository) String() string {
	// generic is an empty string as it could not be predetermined
	return [...]string{
		"",
		"chrome", "firefox", "edge",
		"redroid",
		"windows-chrome", "windows-edge", "windows-firefox",
		"cypress-chrome", "cypress-chromium", "cypress-edge", "cypress-firefox"}[repository]
}

func (repository supportedRepository) GetBrowserName() string {
	return [...]string{
		"",
		"chrome", "firefox", "edge",
		"redroid",
		"chrome", "edge", "firefox",
		"chrome", "chromium", "edge", "firefox"}[repository]
}

func (repository supportedRepository) GetPlatform() envtype.ENV_TYPE {
	return [...]envtype.ENV_TYPE{
		envtype.GENERIC,
		envtype.LINUX, envtype.LINUX, envtype.LINUX,
		envtype.ANDROID,
		envtype.WINDOWS, envtype.WINDOWS, envtype.WINDOWS,
		envtype.CYPRESS, envtype.CYPRESS, envtype.CYPRESS, envtype.CYPRESS}[repository]
}

func RepositoryFromString(repName string) (supportedRepository, error) {
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
	case WINDOWS_FIREFOX.String():
		return WINDOWS_FIREFOX, nil
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
