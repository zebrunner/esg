package capabilities

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	preConfigurationCapsProcessor  CapsProcessor
	postConfigurationCapsProcessor CapsProcessor
	vendorCapNames                 = []string{
		"enableVNC",
		"enableVideo",
		"enableLog",
		"enableDebug",
		"idleTimeout",
		"maxTimeout",
		"screenResolution",
		"videoScreenSize",
		"videoCodec",
		"frameRate",
		"deviceName",
		"cpu", "Cpu", //to support lower case and camel case
		"memory", "Memory", //to support lower case and camel case
		"timeZone",
		"env",
		"hostEntries",
		"dnsServers",
		"mitm", "Mitm", //to support lower case and camel case
		"mitmArgs", "MitmArgs", "mitmargs",
		"mitmCpu", "MitmCpu", "mitmcpu",
		"mitmMemory", "MitmMemory", "mitmmemory",
	}
)

func init() {
	preConfigurationCapsProcessor = CapsProcessor{
		"platform": {
			KeyProcessor:   replaceName("platformName"),
			ValueProcessor: func(x interface{}) interface{} { return strings.ToLower(x.(string)) },
			Validator:      validatePlatforms("linux", "windows", "any"),
		},
		"name": {
			KeyProcessor: replaceName("browserName"),
		},
		"version": {
			KeyProcessor: replaceName("browserVersion"),
		},
		"goog:chromeOptions": {
			ValueProcessor: deletePref("download.default_directory"),
		},
		"ms:edgeOptions": {
			ValueProcessor: deletePref("download.default_directory"),
		},
		"prefs": {
			ValueProcessor: deletePref("download.default_directory"),
		},
		"moz:firefoxOptions": {
			ValueProcessor: deletePrefFromProfile("browser.download.dir"),
		},
	}

	for _, name := range vendorCapNames {
		preConfigurationCapsProcessor[config.VendorPrefix+":"+name] = &Processors{
			KeyProcessor: replaceName(name),
		}
	}

	postConfigurationCapsProcessor = CapsProcessor{
		"goog:chromeOptions": {
			ValueProcessor: addArg("remote-allow-origins", "--remote-allow-origins=*"),
		},
		"mitm": {
			NewCapabilitiesGenerator: func(value interface{}) (bool, map[string]interface{}) {
				if boolValue, ok := value.(bool); ok && boolValue {
					capabilityToAdd := map[string]interface{}{
						"proxy": map[string]interface{}{
							"httpProxy": "mitm:8080",
							"sslProxy":  "mitm:8080",
							"proxyType": "manual",
						},
					}

					return true, capabilityToAdd
				}
				return false, nil
			},
		},
		"browserVersion": {
			DeleteCapabilityProcessor: func(value interface{}) bool {
				if version, ok := value.(string); ok {
					version = strings.ToLower(version)
					return version == "latest" || version == "null" || version == ""
				}

				return true
			},
		},
		"platformName": {
			NewCapabilitiesGenerator: func(value interface{}) (bool, map[string]interface{}) {
				if platform, ok := value.(string); ok {
					platform = strings.ToLower(platform)
					if platform == "windows" {
						return true, map[string]interface{}{
							"browserVersion": "*",
						}
					}
				}
				return false, nil
			},
		},
	}
}

type CapsProcessor map[string]*Processors

type Processors struct {
	// replace capabilites key by passed func
	KeyProcessor              func(string) string
	ValueProcessor            func(interface{}) interface{}
	Validator                 func(interface{}) error
	DeleteCapabilityProcessor func(interface{}) bool
	NewCapabilitiesGenerator  func(value interface{}) (bool, map[string]interface{})
}

func replaceName(name string) func(string) string {
	return func(_ string) string {
		return name
	}
}

func validatePlatforms(allowedPlatforms ...string) func(interface{}) error {
	return func(value interface{}) error {
		for _, platform := range allowedPlatforms {
			if platform == value.(string) {
				return nil
			}
		}
		return fmt.Errorf("platform not allowed")
	}
}

func deletePref(prefKey string) func(interface{}) interface{} {
	return func(options interface{}) interface{} {
		if optionsMap, ok := options.(map[string]interface{}); ok {

			deleteF := func(dir map[string]interface{}) {
				if downlaodDir := dir[prefKey]; downlaodDir != nil {
					delete(dir, prefKey)
				}
			}

			if prefMap, ok := optionsMap["prefs"].(map[string]interface{}); ok {
				deleteF(prefMap)
			}

			deleteF(optionsMap)
		}
		return options
	}
}

func deletePrefFromProfile(prefKey string) func(interface{}) interface{} {
	return func(options interface{}) interface{} {
		if optionsMap, ok := options.(map[string]interface{}); ok {
			if profileEncoded, ok := optionsMap["profile"].(string); ok {
				profileBytes, err := base64.StdEncoding.DecodeString(profileEncoded)
				if err != nil {
					return options
				}

				profilesMap, err := unzipFFProfile(profileBytes)
				if err != nil {
					return options
				}

				for name, prefernces := range profilesMap {
					for i := 0; i < len(prefernces); i++ {
						if strings.Contains(prefernces[i], prefKey) {
							profilesMap[name] = append(prefernces[:i], prefernces[i+1:]...)
							break
						}
					}
				}

				zippedBuf, err := zipFFProfile(profilesMap)
				if err != nil {
					return options
				}
				optionsMap["profile"] = base64.StdEncoding.EncodeToString(zippedBuf.Bytes())
			}
		}
		return options
	}
}

func addArg(argToRewrite string, fullArg string) func(interface{}) interface{} {
	return func(options interface{}) interface{} {
		if optionsMap, ok := options.(map[string]interface{}); ok {
			if args, ok := optionsMap["args"].([]interface{}); ok {
				for i, v := range args {
					argStr := v.(string)
					if strings.Contains(argStr, argToRewrite) {
						args[i] = fullArg
						return options
					}
				}
				optionsMap["args"] = append(args, fullArg)
			}
		}
		return options
	}
}

func (capsProcessor CapsProcessor) applyProcessors(caps map[string]interface{}) error {
	for name, value := range caps {
		newKey := name
		newValue := value

		if processor := capsProcessor[name]; processor != nil {
			if processor.KeyProcessor != nil {
				newKey = processor.KeyProcessor(name)
			}
			if processor.ValueProcessor != nil {
				newValue = processor.ValueProcessor(value)
			}
			if processor.Validator != nil {
				err := processor.Validator(newValue)
				if err != nil {
					return err
				}
			}
			if processor.NewCapabilitiesGenerator != nil {
				if toAdd, capabilities := processor.NewCapabilitiesGenerator(newValue); toAdd {
					for k, v := range capabilities {
						caps[k] = v
					}
				}
			}

			toDelete := processor.DeleteCapabilityProcessor != nil && processor.DeleteCapabilityProcessor(newValue)
			if toDelete {
				delete(caps, newKey)
			} else {
				if newKey != name {
					delete(caps, name)
				}
				caps[newKey] = newValue
			}
		}
	}
	return nil
}

func ParseRequestCapabilities(body io.ReadCloser) (*RequestCaps, *Capabilities, error) {
	reqCaps := &RequestCaps{}
	err := json.NewDecoder(body).Decode(reqCaps)
	if err != nil {
		return nil, nil, fmt.Errorf("bad json format: %v", err)
	}

	reqCaps.processAllCapabilititesByFunc(unpackVendorOptions)

	err = reqCaps.processAllCapabilititesByFunc(preConfigurationCapsProcessor.applyProcessors)
	if err != nil {
		return nil, nil, err
	}

	configurationCapabilities, err := reqCaps.GetContainerConfiguration()
	if err != nil {
		return nil, nil, err
	}

	err = reqCaps.processAllCapabilititesByFunc(postConfigurationCapsProcessor.applyProcessors)
	if err != nil {
		return nil, nil, err
	}

	return reqCaps, configurationCapabilities, nil
}

type RequestCaps struct {
	Capabilities struct {
		AlwaysMatch map[string]interface{}   `json:"alwaysMatch,omitempty"`
		FirstMatch  []map[string]interface{} `json:"firstMatch,omitempty"`
	} `json:"capabilities,omitempty"`
	DesiredCapabilities map[string]interface{} `json:"desiredCapabilities,omitempty"`
}

func (c *RequestCaps) ToRequestBody() (*bytes.Reader, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return nil, err
	}

	return bytes.NewReader(body), nil
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

func (c *RequestCaps) processAllCapabilititesByFunc(fn func(caps map[string]interface{}) error) error {
	var err error
	if c.DesiredCapabilities != nil {
		err = fn(c.DesiredCapabilities)
		if err != nil {
			return err
		}
	}

	if c.Capabilities.AlwaysMatch != nil {
		err = fn(c.Capabilities.AlwaysMatch)
		if err != nil {
			return err
		}
	}

	for _, fmCaps := range c.Capabilities.FirstMatch {
		err = fn(fmCaps)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *RequestCaps) GetContainerConfiguration() (*Capabilities, error) {
	containerConfiguration := GetDefaultCaps()

	if c.DesiredCapabilities != nil {
		desiredCaps := RemovePrefix(c.DesiredCapabilities, "appium")
		err := containerConfiguration.ParseRequestCaps(desiredCaps)
		if err != nil {
			log.WithError(err).Warn("Failed to map config")
			return nil, err
		}
	}

	if c.Capabilities.AlwaysMatch != nil {
		amCaps := RemovePrefix(c.Capabilities.AlwaysMatch, "appium")
		err := containerConfiguration.ParseRequestCaps(amCaps)
		if err != nil {
			log.WithError(err).Warn("Failed to map config")
			return nil, err
		}
	}

	for _, fmCaps := range c.Capabilities.FirstMatch {
		caps := RemovePrefix(fmCaps, "appium")
		err := containerConfiguration.ParseRequestCaps(caps)
		if err != nil {
			log.WithError(err).Warn("Failed to map config")
			return nil, err
		}
	}

	return containerConfiguration, nil
}

func unpackVendorOptions(caps map[string]interface{}) error {
	if zebrunnerOptions, ok := caps["zebrunner:options"].(map[string]interface{}); ok {
		for _, name := range vendorCapNames {
			if value, ok := zebrunnerOptions[name]; ok {
				caps[config.VendorPrefix+":"+name] = value
			}
		}
		delete(caps, "zebrunner:options")
	}
	return nil
}

func unzipFFProfile(profiles []byte) (map[string][]string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(profiles), int64(len(profiles)))
	if err != nil {
		return nil, err
	}

	profilesMap := make(map[string][]string, 0)
	for _, zipFile := range zipReader.File {
		file, err := zipFile.Open()
		if err != nil {
			return nil, err
		}
		defer file.Close()
		scanner := bufio.NewScanner(file)
		prefs := make([]string, 0)
		for scanner.Scan() {
			prefs = append(prefs, scanner.Text())
		}
		// key - profileName; value - prefernces array
		profilesMap[zipFile.Name] = prefs
	}

	return profilesMap, nil
}

func zipFFProfile(profilesMap map[string][]string) (*bytes.Buffer, error) {
	zippedBuf := bytes.NewBuffer(make([]byte, 0))
	zipWriter := zip.NewWriter(zippedBuf)
	for k, v := range profilesMap {
		w, err := zipWriter.Create(k)
		if err != nil {
			return nil, err
		}
		profileStr := strings.Join(v, "\n")
		_, err = io.Copy(w, bytes.NewReader([]byte(profileStr)))
		if err != nil {
			return nil, err
		}
	}
	zipWriter.Close()
	return zippedBuf, nil
}

func RemovePrefix(caps map[string]interface{}, prefix string) map[string]interface{} {
	newCaps := map[string]interface{}{}
	for key, value := range caps {
		newCaps[strings.TrimPrefix(key, prefix+":")] = value
	}
	return newCaps
}
