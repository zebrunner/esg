package selenium

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/imdario/mergo"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"strings"
)

type ContainerConfiguration struct {
	BrowserName      string
	BrowserVersion   string
	PlatformName     string
	Proxy            map[string]interface{}
	Timeouts         string
	EnableVnc        bool
	EnableVideo      bool
	EnableLog        bool
	ScreenResolution string
	DeviceName       string
	Skin             string
	CpuResource      int64
	MemoryResource   int64
	IdleTimeout      int64
	VideoCodec       string
	TimeZone         string
	Env              []string
	HostEntries      []string
	DNSServers       []string
}

type LegacyCaps struct {
	Name     string
	Platform string
	Version  string
}

func RemovePrefix(caps map[string]interface{}) map[string]interface{} {
	newCaps := map[string]interface{}{}
	for key, value := range caps {
		newCaps[strings.TrimPrefix(key, config.VendorPrefix+":")] = value
	}
	return newCaps
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

func GetContainerConfiguration(caps map[string]interface{}) (*ContainerConfiguration, error) {
	conf := ContainerConfiguration{}

	capabilities, ok := caps["capabilities"].(map[string]interface{})
	if !ok {
		return nil, errors.New("caps invalid")
	}

	alwaysMatch, ok := capabilities["alwaysMatch"].(map[string]interface{})
	if ok {
		alwaysMatch = RemovePrefix(alwaysMatch)
		amConf, err := MapConfig(alwaysMatch)
		if err != nil {
			log.WithError(err).Warn("Failed to map config")
		}

		err = mergo.Merge(&conf, amConf)
		if err != nil {
			log.WithError(err).Warn("Failed to map config")
		}
	}

	firstMatch, ok := capabilities["firstMatch"].([]map[string]interface{})
	if !ok {
		return nil, errors.New("caps invalid")
	}
	for _, fmCaps := range firstMatch {
		fmCapsMap := fmCaps
		//fmCapsMap, ok := fmCaps.(map[string]interface{})
		//if !ok {
		//	continue
		//}
		fmCapsMap = RemovePrefix(fmCapsMap)
		fmConf, err := MapConfig(fmCapsMap)
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

type CapProcessor struct {
	KeyProcessor   func(string) string
	ValueProcessor func(interface{}) interface{}
	Validator      func(interface{}) error
}

func ProcessCapabilities(caps map[string]interface{}) (map[string]interface{}, error) {
	return nil, nil
}

func replaceName(name string) func(string) string {
	return func (_ string) string {
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

func processLegacyCaps(caps map[string]interface{}) (map[string]interface{}, error) {
	allowedPlatforms := []string{"linux"}
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

func PreprocessCapabilities(caps map[string]interface{}) (*map[string]interface{}, error) {
	capsRequest, ok := caps["capabilities"].(map[string]interface{})
	if !ok {
		return nil, errors.New("caps validation")
	}

	desiredCaps, ok := caps["desiredCapabilities"].(map[string]interface{})
	if !ok {
		return nil, errors.New("caps validation")
	}

	requiredCaps := map[string]interface{}{}
	if alwaysMatch, ok := capsRequest["alwaysMatch"].(map[string]interface{}); ok {
		requiredCaps = alwaysMatch
	}

	firstMatchCaps := []map[string]interface{}{}
	if firstMatch, ok := capsRequest["firstMatch"].([]interface{}); ok {
		for i, v := range firstMatch {
			if c, ok := v.(map[string]interface{}); ok {
				firstMatchCaps = append(firstMatchCaps, c)
			} else {
				log.Warnf("Failed to process firstMatch capabilities at position %d", i)
			}
		}
	}

	processedDesiredCaps := map[string]interface{}{}
	for k, v := range desiredCaps {
		processedDesiredCaps[k] = v
	}

	// Process desired caps
	processedDesiredCaps, err := processLegacyCaps(processedDesiredCaps)
	if err != nil {
		return nil, err
	}
	processedDesiredCaps, err = processVendorCaps(processedDesiredCaps)
	if err != nil {
		return nil, err
	}

	// Remove what already present in alwaysMatch
	for key, _ := range requiredCaps {
		delete(processedDesiredCaps, key)
	}

	// Add vendor caps to all from firstMatch
	for _, fmCaps := range firstMatchCaps {
		for name, value := range processedDesiredCaps {
			if strings.Contains(name, ":") && requiredCaps[name] == nil {
				fmCaps[name] = value
			}
		}
	}

	// TODO: do I need to validate caps here?

	capabilities := map[string]interface{}{}
	if len(requiredCaps) != 0 {
		capabilities["alwaysMatch"] = requiredCaps
	}
	capabilities["firstMatch"] = firstMatchCaps

	return &map[string]interface{}{
		"capabilities":        capabilities,
		"desiredCapabilities": desiredCaps,
	}, nil
}

func (c *ContainerConfiguration) Memory() int64 {
	memory := int64(config.MinMemory)
	if c.MemoryResource > memory {
		memory = c.MemoryResource
	}
	if memory > int64(config.MaxMemory) {
		memory = int64(config.MaxMemory)
	}
	return memory
}

func (c *ContainerConfiguration) Cpu() int64 {
	cpu := int64(config.MinCpu)
	if c.CpuResource > cpu {
		cpu = c.CpuResource
	}
	if cpu > int64(config.MaxCpu) {
		cpu = int64(config.MaxCpu)
	}
	return cpu
}

// Browser configuration
type Browser struct {
	Image string
	Path  string
	Port  int64
}

func (c *ContainerConfiguration) Browser() Browser {
	browser := c.BrowserName
	version := strings.ToLower(c.BrowserVersion)
	log.WithFields(log.Fields{
		"browser": browser,
		"version": version,
	}).Info("Locating service")

	org := "public.ecr.aws/zebrunner" //public zebrunner ECR docker registry
	if browser == "MicrosoftEdge" {
		browser = "edge"
	}

	if browser == "operablink" {
		browser = "opera"
	}

	useAsLatest := []string{
		"null",
		"latest",
		"",
	}

	for _, item := range useAsLatest {
		if item == version {
			version = "latest"
			break
		}
	}

	image := fmt.Sprintf("%s/%s:%s", org, browser, version)

	path := ""
	if browser == "firefox" {
		path = "/wd/hub"
	}

	return Browser{
		Image: image,
		Path:  path,
		Port:  4444,
	}
}
