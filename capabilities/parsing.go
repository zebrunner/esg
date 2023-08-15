package capabilities

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"io"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

type CapProcessor struct {
	KeyProcessor        func(string) string
	ValueProcessor      func(interface{}) interface{}
	KeyByValueProcessor func(interface{}) string
	Validator           func(interface{}) error
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

func applyProcessor(caps map[string]interface{}, processors map[string]*CapProcessor) error {
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
			if processor.KeyByValueProcessor != nil {
				newKey = processor.KeyByValueProcessor(value)
			}
			if processor.Validator != nil {
				err := processor.Validator(newValue)
				if err != nil {
					return err
				}
			}
		}

		if newKey != name {
			delete(caps, name)
		}

		if newKey != "" {
			caps[newKey] = newValue
		}
	}
	return nil
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

func (c *RequestCaps) Process() error {
	var err error
	err = processLegacyCaps(c.Capabilities.AlwaysMatch)
	if err != nil {
		return err
	}

	err = processVendorCaps(c.Capabilities.AlwaysMatch)
	if err != nil {
		return err
	}

	err = processCaps(c.Capabilities.AlwaysMatch)
	if err != nil {
		return err
	}

	err = processOptions(c.Capabilities.AlwaysMatch)
	if err != nil {
		return err
	}

	processProxy(c.Capabilities.AlwaysMatch)

	for index := range c.Capabilities.FirstMatch {
		err = processLegacyCaps(c.Capabilities.FirstMatch[index])
		if err != nil {
			return err
		}

		err = processVendorCaps(c.Capabilities.FirstMatch[index])
		if err != nil {
			return err
		}

		err = processCaps(c.Capabilities.FirstMatch[index])
		if err != nil {
			return err
		}

		err = processOptions(c.Capabilities.FirstMatch[index])
		if err != nil {
			return err
		}

		processProxy(c.Capabilities.FirstMatch[index])
	}

	return nil
}

func (c *RequestCaps) ProcessLegacy() error {
	// Process desired caps
	err := processLegacyCaps(c.DesiredCapabilities)
	if err != nil {
		return err
	}

	err = processVendorCaps(c.DesiredCapabilities)
	if err != nil {
		return err
	}

	err = processCaps(c.DesiredCapabilities)
	if err != nil {
		return err
	}

	err = processOptions(c.DesiredCapabilities)
	if err != nil {
		return err
	}

	if c.Capabilities.AlwaysMatch != nil {
		for k := range c.Capabilities.AlwaysMatch {
			delete(c.DesiredCapabilities, k)
		}

		err = processCaps(c.Capabilities.AlwaysMatch)
		if err != nil {
			return err
		}
	}

	// Add vendor and option caps to all from firstMatch
	for _, fmCaps := range c.Capabilities.FirstMatch {
		for name, value := range c.DesiredCapabilities {
			if strings.Contains(name, ":") {
				fmCaps[name] = value
			}
		}

		processProxy(fmCaps)

		renamedLegacy := []string{"browserName", "platformName", "browserVersion"}
		for _, name := range renamedLegacy {
			if fmCaps[name] == nil && c.DesiredCapabilities[name] != nil {
				fmCaps[name] = c.DesiredCapabilities[name]
			}
		}

		err = processCaps(fmCaps)
		if err != nil {
			return err
		}
	}

	return nil
}

func (c *RequestCaps) GetContainerConfiguration() (*Capabilities, error) {
	conf := GetDefaultCaps()

	amCaps := RemovePrefix(c.Capabilities.AlwaysMatch, config.VendorPrefix)
	amCaps = RemovePrefix(amCaps, "appium")
	err := conf.ParseRequestCaps(amCaps)
	if err != nil {
		log.WithError(err).Warn("Failed to map config")
		return nil, err
	}

	for _, fmCaps := range c.Capabilities.FirstMatch {
		caps := RemovePrefix(fmCaps, config.VendorPrefix)
		caps = RemovePrefix(caps, "appium")

		conf.ParseRequestCaps(caps)
		if err != nil {
			log.WithError(err).Warn("Failed to map config")
			return nil, err
		}
	}

	return conf, nil
}

func processLegacyCaps(caps map[string]interface{}) error {
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

	err := applyProcessor(caps, legacyProcessors)
	if err != nil {
		return err
	}

	return nil
}

func processVendorCaps(caps map[string]interface{}) error {
	vendorCapNames := []string{
		"enableVNC",
		"enableVideo",
		"enableLog",
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
	processors := map[string]*CapProcessor{}
	for _, name := range vendorCapNames {
		processors[name] = &CapProcessor{
			KeyProcessor: addPrefix(config.VendorPrefix),
		}
	}

	//overrided vendor caps by existing w3c caps
	if zebrunnerOptions, ok := caps["zebrunner:options"].(map[string]interface{}); ok {
		for k, v := range zebrunnerOptions {
			caps[k] = v
			processors[k] = &CapProcessor{
				KeyProcessor: addPrefix(config.VendorPrefix),
			}
		}
	}

	err := applyProcessor(caps, processors)
	if err != nil {
		return err
	}

	return nil
}

func processCaps(caps map[string]interface{}) error {
	processors := map[string]*CapProcessor{
		"browserVersion": {
			KeyByValueProcessor: func(value interface{}) string {
				version, ok := value.(string)
				if ok {
					version = strings.ToLower(version)
					if version == "latest" || version == "null" || version == "" {
						return ""
					}
				}
				return "browserVersion"
			},
		},
	}

	err := applyProcessor(caps, processors)
	if err != nil {
		return err
	}

	return nil
}

func processProxy(caps map[string]interface{}) {
	for key, value := range caps {
		if strings.ToLower(key) == "zebrunner:mitm" {
			if enabled, ok := value.(bool); !ok || !enabled {
				return
			}

			log.Debug("Found mitm cap, overriding proxy capabilities object...")
			// proxy:map[sslProxy:mitm:8080 httpProxy:mitm:8080 proxyType:MANUAL]
			caps["proxy"] = map[string]interface{}{
				"httpProxy": "mitm:8080",
				"sslProxy":  "mitm:8080",
				"proxyType": "manual",
			}
			return
		}
	}
}

func processOptions(caps map[string]interface{}) error {
	downloadOptionProcessors := map[string]*CapProcessor{
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

	err := applyProcessor(caps, downloadOptionProcessors)
	if err != nil {
		return err
	}

	argsProcessors := map[string]*CapProcessor{
		"goog:chromeOptions": {
			ValueProcessor: func(options interface{}) interface{} {
				if optionsMap, ok := options.(map[string]interface{}); ok {
					if args, ok := optionsMap["args"].([]interface{}); ok {
						for i, v := range args {
							argStr := v.(string)
							if strings.Contains(argStr, "--remote-allow-origins") {
								args[i] = "--remote-allow-origins=*"
								return options
							}
						}
						optionsMap["args"] = append(args, "--remote-allow-origins=*")
					}
				}
				return options
			},
		},
	}

	err = applyProcessor(caps, argsProcessors)
	if err != nil {
		return err
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
