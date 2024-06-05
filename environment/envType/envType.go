package envtype

import (
	"fmt"

	"github.com/zebrunner/esg/capabilities"
)

type ENV_TYPE int

// all supported envs
const (
	GENERIC ENV_TYPE = iota
	LINUX
	WINDOWS
	CYPRESS
	ANDROID
	ANY
)

func (e ENV_TYPE) String() string {
	return [...]string{"generic", "linux", "windows", "cypress", "android", "any"}[e]
}

type capsForPlatform func(string, string) ([]*capabilities.Capabilities, error)

func (env ENV_TYPE) GetMockCapsBuilder() (capsForPlatform, error) {
	switch env {
	case LINUX:
		return capsForLinux, nil
	case WINDOWS:
		return capsForWindows, nil
	case CYPRESS:
		return capsForCypress, nil
	case ANDROID:
		return capsForAndroid, nil
	default:
		return nil, fmt.Errorf("environment is not supported. env=%s", env.String())
	}
}

func capsForLinux(name string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	reqCaps := map[string]interface{}{
		"platformName":   LINUX.String(),
		"browserName":    name,
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

func capsForWindows(name string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	reqCaps := map[string]interface{}{
		"platformName":   WINDOWS.String(),
		"browserName":    name,
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

func capsForCypress(name string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	caps := capabilities.GetDefaultCaps()
	reqCaps := map[string]interface{}{
		"platformName":   CYPRESS.String(),
		"browserName":    name,
		"browserVersion": version,
	}

	err := caps.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	return append(capsList, caps), nil
}

func capsForAndroid(name string, version string) ([]*capabilities.Capabilities, error) {
	capsList := make([]*capabilities.Capabilities, 0)
	caps := capabilities.GetDefaultCaps()
	reqCaps := map[string]interface{}{
		"platformName":    ANDROID.String(),
		"deviceName":      name,
		"platformVersion": version,
	}

	err := caps.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	return append(capsList, caps), nil
}
