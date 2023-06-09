package capabilities

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/imdario/mergo"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
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

func deletePref(prefKey string) func(interface{}) interface{} {
	return func(options interface{}) interface{} {
		if optionsMap, ok := options.(map[string]interface{}); ok {

			deleteF := func(dir map[string]interface{}){
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

	processedDesiredCaps, err = processOptions(processedDesiredCaps)
	if err != nil {
		return err
	}

	if c.Capabilities.AlwaysMatch != nil {
		for k := range c.Capabilities.AlwaysMatch {
			delete(processedDesiredCaps, k)
		}
	}

	// Add vendor and option caps to all from firstMatch
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

func (c *RequestCaps) GetContainerConfiguration() (*Capabilities, error) {
	conf := Capabilities{}
	validationErr := errors.New("wrong capabilities format")

	amCaps := RemovePrefix(c.Capabilities.AlwaysMatch, config.VendorPrefix)
	amCaps = RemovePrefix(amCaps, "appium")
	amConf, err := MapConfig(amCaps)
	if err != nil {
		log.WithError(err).Warn("Failed to map config")
		return nil, validationErr
	}

	err = mergo.Merge(&conf, amConf)
	if err != nil {
		log.WithError(err).Warn("Failed to map config")
		return nil, validationErr
	}

	for _, fmCaps := range c.Capabilities.FirstMatch {
		caps := RemovePrefix(fmCaps, config.VendorPrefix)
		caps = RemovePrefix(caps, "appium")

		fmConf, err := MapConfig(caps)
		if err != nil {

			return nil, validationErr
		}
		err = mergo.Merge(&conf, fmConf)
		if err != nil {
			return nil, validationErr
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
		"enableVNC",
		"enableVideo",
		"enableLog",
		"idleTimeout",
		"maxTimeout",
		"screenResolution",
		"deviceName",
		"cpu", "Cpu", //to support lower case and camel case
		"memory", "Memory", //to support lower case and camel case
		"timeZone",
		"env",
		"hostEntries",
		"dnsServers",
		"mitm", "Mitm", //to support lower case and camel case
		"mitmArgs", "MitmArgs", "mitmargs",
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

func processOptions(caps map[string]interface{}) (map[string]interface{}, error) {
	optionProcessors := map[string]*CapProcessor{
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
			ValueProcessor: func(options interface{}) interface{} {
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
								if strings.Contains(prefernces[i], "browser.download.dir") {
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
			},
		},
	}

	newCaps, err := applyProcessor(caps, optionProcessors)
	if err != nil {
		return nil, err
	}

	return newCaps, nil
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

func MapConfig(m map[string]interface{}) (*Capabilities, error) {
	jsonCaps, err := json.Marshal(m)
	if err != nil {
		log.WithError(err).Warn("Failed to serialize caps")
		return nil, err
	}

	conf := Capabilities{}
	err = json.Unmarshal(jsonCaps, &conf)
	if err != nil {
		log.WithError(err).Warn("Failed to deserialize caps")
		return nil, err
	}

	return &conf, nil
}

func RemovePrefix(caps map[string]interface{}, prefix string) map[string]interface{} {
	newCaps := map[string]interface{}{}
	for key, value := range caps {
		newCaps[strings.TrimPrefix(key, prefix+":")] = value
	}
	return newCaps
}
