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
		newCaps[strings.TrimPrefix(key, config.VendorPrefix + ":")] = value
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
	if !ok {
		return nil, errors.New("caps invalid")
	}
	alwaysMatch = RemovePrefix(alwaysMatch)
	amConf, err := MapConfig(alwaysMatch)
	if err != nil {
		return nil, err
	}

	err = mergo.Merge(&conf, amConf)

	firstMatch, ok := capabilities["firstMatch"].([]interface{})
	if !ok {
		return nil, errors.New("caps invalid")
	}
	for _, fmCaps := range firstMatch {
		fmCapsMap, ok := fmCaps.(map[string]interface{})
		if !ok {
			continue
		}
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

func PreprocessCapabilities(caps map[string]interface{}) (*map[string]interface{}, error) {
	capsRequest, ok := caps["capabilities"].(map[string]interface{})
	if !ok {
		return nil, errors.New("caps validation")
	}

	desiredCaps, ok := caps["desiredCapabilities"].(map[string]interface{})
	if !ok {
		return nil, errors.New("caps validation")
	}

	requiredCaps, ok := capsRequest["alwaysMatch"].(map[string]interface{})
	if !ok {
		requiredCaps = map[string]interface{}{}
	}

	firstMatchCaps, ok := capsRequest["firstMatch"].([]interface{})
	if !ok {
		firstMatchCaps = []interface{}{map[string]interface{}{}}
	}

	newCaps := ProcessLegacyCaps(desiredCaps)
	for _, fmCaps := range firstMatchCaps {
		fmCapsMap, ok := fmCaps.(map[string]interface{})
		if !ok {
			continue
		}
		err := mergo.Merge(&fmCapsMap, newCaps, mergo.WithOverride)
		if err != nil {
			log.WithError(err).Warn("Failed to merge caps")
			continue
		}
		fmCaps = fmCapsMap
	}

	// TODO: do I need to validate caps here?

	requiredCaps["browserName"] = "firefox"

	resultCaps := map[string]interface{}{
		"capabilities": map[string]interface{}{
			"alwaysMatch": requiredCaps,
			"firstMatch": firstMatchCaps,
		},
		"desiredCapabilities": desiredCaps,
	}
	return &resultCaps, nil
}

func ProcessLegacyCaps(desiredCaps map[string]interface{}) map[string]interface{} {
	// Support for legacy capabilities used by some clients
	legacyMappings := map[string]string{
		"name": "browserName",
		"version": "browserVersion",
		"platform": "platformName",
	}
	vendorMappings := map[string]string{
		"enableVnc": config.VendorPrefix + ":" + "enableVnc",
		"enableVideo": config.VendorPrefix + ":" + "enableVideo",
		"enableLog": config.VendorPrefix + ":" + "enableLog",
		"idleTimeout": config.VendorPrefix + ":" + "idleTimeout",
		"screenResolution": config.VendorPrefix + ":" + "screenResolution",
		"deviceName": config.VendorPrefix + ":" + "deviceName",
		"skin": config.VendorPrefix + ":" + "skin",
		"cpu": config.VendorPrefix + ":" + "cpu",
		"memory": config.VendorPrefix + ":" + "memory",
		"videoCodec": config.VendorPrefix + ":" + "videoCodec",
		"timeZone": config.VendorPrefix + ":" + "timeZone",
		"env": config.VendorPrefix + ":" + "env",
		"hostEntries": config.VendorPrefix + ":" + "hostEntries",
		"dnsServers": config.VendorPrefix + ":" + "dnsServers",
	}

	newCaps := map[string]interface{}{}
	for oldKey, newKey := range legacyMappings {
		value := desiredCaps[oldKey]
		if value != nil {
			newCaps[newKey] = value
		}
	}

	for oldKey, newKey := range vendorMappings {
		value := desiredCaps[oldKey]
		if value != nil {
			newCaps[newKey] = value
		}
	}

	return newCaps
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
