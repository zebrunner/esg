package selenium

import (
	"encoding/json"
	"errors"
	"github.com/imdario/mergo"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"strings"
)


type CapProcessor struct {
	KeyProcessor   func(string) string
	ValueProcessor func(interface{}) interface{}
	Validator      func(interface{}) error
}

func replaceName(name string) func(string) string {
	return func(_ string) string {
		return name
	}
}

func addPrefix(prefix string) func(string) string {
	return func(name string) string {
		return prefix + ":" + name
	}
}

func applyProcessor(caps map[string]interface{}, processors map[string]*CapProcessor) (map[string]interface{}, error) {
	newCaps := map[string]interface{}{}
	for name, value := range caps {
		newKey := name
		newValue := value

		if processor := processors[name]; processor != nil {
			if processor.KeyProcessor != nil {
				newKey = processor.KeyProcessor(name)
			}
			if processor.ValueProcessor != nil {
				newValue = processor.ValueProcessor(value)
			}
			if processor.Validator != nil {
				err := processor.Validator(newValue)
				if err != nil {
					return nil, err
				}
			}
		}

		newCaps[newKey] = newValue
	}
	return newCaps, nil
}

type RequestCaps struct {
	Capabilities struct {
		AlwaysMatch map[string]interface{}   `json:"alwaysMatch,omitempty"`
		FirstMatch  []map[string]interface{} `json:"firstMatch,omitempty"`
	} `json:"capabilities,omitempty"`
	DesiredCapabilities map[string]interface{} `json:"desiredCapabilities,omitempty"`
}

func (c *RequestCaps) ToMap() map[string]interface{} {
	return map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": c.Capabilities.AlwaysMatch,
			"firstMatch":  c.Capabilities.FirstMatch,
		},
		"desiredCapabilities": c.DesiredCapabilities,
	}
}

func (c *RequestCaps) ProcessLegacy() error {
	processedDesiredCaps := map[string]interface{}{}
	for k, v := range c.DesiredCapabilities {
		processedDesiredCaps[k] = v
	}

	// Process desired caps
	processedDesiredCaps, err := processLegacyCaps(processedDesiredCaps)
	if err != nil {
		return err
	}
	processedDesiredCaps, err = processVendorCaps(processedDesiredCaps)
	if err != nil {
		return err
	}

	if c.Capabilities.AlwaysMatch != nil {
		for k, _ := range c.Capabilities.AlwaysMatch {
			delete(processedDesiredCaps, k)
		}
	}

	// Add vendor caps to all from firstMatch
	for _, fmCaps := range c.Capabilities.FirstMatch {
		for name, value := range processedDesiredCaps {
			if strings.Contains(name, ":") {
				fmCaps[name] = value
			}
		}

		renamedLegacy := []string{"browserName", "platformName", "browserVersion"}
		for _, name := range renamedLegacy {
			if fmCaps[name] == nil && processedDesiredCaps[name] != nil {
				fmCaps[name] = processedDesiredCaps[name]
			}
		}

	}

	// Replace latest version
	if c.Capabilities.AlwaysMatch != nil && c.Capabilities.AlwaysMatch["browserName"] == "firefox" {
		version, ok := c.Capabilities.AlwaysMatch["browserVersion"].(string)
		version = strings.ToLower(version)
		if ok && (version == "latest" || version == "null" || version == "") {
			delete(c.Capabilities.AlwaysMatch, "browserVersion")
		}
	}

	for _, fmCaps := range c.Capabilities.FirstMatch {
		version, ok := fmCaps["browserVersion"].(string)
		name := fmCaps["browserName"]
		version = strings.ToLower(version)
		if ok && name == "firefox" && (version == "latest" || version == "null" || version == "") {
			delete(fmCaps, "browserVersion")
		}
	}

	return nil
}

func (c *RequestCaps) GetContainerConfiguration() (*ContainerConfiguration, error) {
	conf := ContainerConfiguration{}
	amConf, err := MapConfig(RemovePrefix(c.Capabilities.AlwaysMatch))
	if err != nil {
		log.WithError(err).Warn("Failed to map config")
	}

	err = mergo.Merge(&conf, amConf)
	if err != nil {
		log.WithError(err).Warn("Failed to map config")
	}

	for _, fmCaps := range c.Capabilities.FirstMatch {
		fmConf, err := MapConfig(RemovePrefix(fmCaps))
		if err != nil {
			continue
		}
		err = mergo.Merge(&conf, fmConf)
		if err != nil {
			continue
		}
	}

	return &conf, nil
}

func processLegacyCaps(caps map[string]interface{}) (map[string]interface{}, error) {
	allowedPlatforms := []string{"linux", "any"}
	legacyProcessors := map[string]*CapProcessor{
		"platform": {
			KeyProcessor:   replaceName("platformName"),
			ValueProcessor: func(x interface{}) interface{} { return strings.ToLower(x.(string)) },
			Validator: func(x interface{}) error {
				for _, platform := range allowedPlatforms {
					if platform == x.(string) {
						return nil
					}
				}
				return errors.New("platform not allowed")
			},
		},
		"name": {
			KeyProcessor: replaceName("browserName"),
		},
		"version": {
			KeyProcessor: replaceName("browserVersion"),
		},
	}

	newCaps, err := applyProcessor(caps, legacyProcessors)
	if err != nil {
		return nil, err
	}

	return newCaps, nil
}

func processVendorCaps(caps map[string]interface{}) (map[string]interface{}, error) {
	vendorCapNames := []string{
		"enableVnc",
		"enableVideo",
		"enableLog",
		"idleTimeout",
		"screenResolution",
		"deviceName",
		"skin",
		"cpu",
		"memory",
		"videoCodec",
		"timeZone",
		"env",
		"hostEntries",
		"dnsServers",
	}
	processors := map[string]*CapProcessor{}
	for _, name := range vendorCapNames {
		processors[name] = &CapProcessor{
			KeyProcessor: addPrefix(config.VendorPrefix),
		}
	}

	newCaps, err := applyProcessor(caps, processors)
	if err != nil {
		return nil, err
	}

	return newCaps, nil
}

func MapConfig(m map[string]interface{}) (*ContainerConfiguration, error) {
	jsonCaps, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}

	conf := ContainerConfiguration{}
	err = json.Unmarshal(jsonCaps, &conf)
	if err != nil {
		return nil, err
	}

	return &conf, nil
}


func RemovePrefix(caps map[string]interface{}) map[string]interface{} {
	newCaps := map[string]interface{}{}
	for key, value := range caps {
		newCaps[strings.TrimPrefix(key, config.VendorPrefix+":")] = value
	}
	return newCaps
}
