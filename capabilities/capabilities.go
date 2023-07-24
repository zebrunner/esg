package capabilities

import (
	"errors"
	"fmt"
	"reflect"
	"strconv"

	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

var (
	fullResolutionFormat  = regexp.MustCompile(`^([0-9]+x[0-9]+)x(8|16|24)$`)
	shortResolutionFormat = regexp.MustCompile(`^[0-9]+x[0-9]+$`)
)

type Capabilities struct {
	BrowserName      string
	BrowserVersion   string
	PlatformName     string
	PlatformVersion  string
	Proxy            map[string]interface{}
	Timeouts         string
	EnableVNC        bool
	EnableVideo      bool
	EnableLog        bool
	ScreenResolution string
	DeviceName       string
	IdleTimeout      int64
	MaxTimeout       int64
	TimeZone         string
	Env              []string
	HostsEntries     []string
	DNSServers       []string

	//Vendor caps
	Cpu    int64
	Memory int64
	//Mitm proxy caps
	Mitm       bool   //enabl mitm with har dump and output generation for mitmweb
	MitmArgs   string // list of arguments for mitmdump command. Important: --verbose and --quiet will be appended forcibly
	MitmCpu    int64
	MitmMemory int64

	// generic launcher caps
	RepositoryUrl string
	Branch        string
	Image         string
	LaunchCommand string
	EnvVariables  map[string]string
}

func (c *Capabilities) GetTimeZone() (*time.Location, error) {
	timeZone := time.UTC
	if c.TimeZone != "" {
		tz, err := time.LoadLocation(c.TimeZone)
		if err != nil {
			log.WithError(err).WithField("value", c.GetTimeZone).Warn("Bad timezone specified")
		} else {
			timeZone = tz
		}
	}

	return timeZone, nil
}

func in(image string, images []string) bool {
	for _, s := range images {
		if s == image {
			return true
		}
	}

	return false
}

func FromImage(image string) ([]*Capabilities, error) {
	platforms := map[string][]string{
		"android": {"redroid"},
		"linux":   {"chrome", "firefox", "edge"},
		"cypress": {"cypress-chrome", "cypress-chromium", "cypress-edge", "cypress-firefox"},
	}

	parts := strings.Split(image, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("failed to parse image, image is in invalid format. image=%s", image)
	}

	executor := parts[0]
	version := parts[1]

	capsList := make([]*Capabilities, 0)
	if executor == "redroid" {
		capsList = append(capsList, &Capabilities{
			PlatformName:    "android",
			DeviceName:      "redroid",
			PlatformVersion: version,
		})
	} else if in(executor, platforms["linux"]) {
		capsList = append(capsList, &Capabilities{
			PlatformName:   "linux",
			BrowserName:    executor,
			BrowserVersion: version,
			Mitm:           false,
		})

		capsList = append(capsList, &Capabilities{
			PlatformName:   "linux",
			BrowserName:    executor,
			BrowserVersion: version,
			Mitm:           true,
		})
	} else if in(executor, platforms["cypress"]) {
		capsList = append(capsList, &Capabilities{
			PlatformName:   "cypress",
			BrowserName:    executor,
			BrowserVersion: version,
		})
	} else {
		return nil, fmt.Errorf("failed to build capabilities from unknown image. image=%s", image)
	}

	return capsList, nil
}

func FromRequestCaps(reqCaps map[string]interface{}) (*Capabilities, error) {
	c := &Capabilities{}
	mapping := map[string]interface{}{
		"browserName":      &c.BrowserName,
		"browserVersion":   &c.BrowserVersion,
		"platformName":     &c.PlatformName,
		"platformVersion":  &c.PlatformVersion,
		"proxy":            &c.Proxy,
		"timeouts":         &c.Timeouts,
		"enableVNC":        &c.EnableVNC,
		"enableVideo":      &c.EnableVideo,
		"enableLog":        &c.EnableLog,
		"screenResolution": &c.ScreenResolution,
		"deviceName":       &c.DeviceName,
		"idleTimeout":      &c.IdleTimeout,
		"maxTimeout":       &c.MaxTimeout,
		"timeZone":         &c.TimeZone,
		"env":              &c.Env,
		"hostsEntries":     &c.HostsEntries,
		"DNSServers":       &c.DNSServers,

		"cpu":    &c.Cpu,
		"memory": &c.Memory,

		"mitm":       &c.Mitm,
		"mitmArgs":   &c.MitmArgs,
		"mitmCpu":    &c.MitmCpu,
		"mitmMemory": &c.MitmMemory,

		"RepositoryUrl": &c.RepositoryUrl,
		"Branch":        &c.Branch,
		"Image":         &c.Image,
		"LaunchCommand": &c.LaunchCommand,
		"EnvVariables":  &c.EnvVariables,
	}

	errs := make([]string, 0)
	for reqKey, reqValue := range reqCaps {
		if capPtr := mapping[reqKey]; capPtr != nil {
			capValue := reflect.Indirect(reflect.ValueOf(capPtr))

			reqValueKind := reflect.TypeOf(reqValue).Kind()
			capValueKind := capValue.Kind()
			if capValueKind == reqValueKind {
				capValue.Set(reflect.ValueOf(reqValue))
				continue
			}

			if capValueKind == reflect.Int64 {
				switch reqValueKind {
				case reflect.Float64:
					reqValue = int64(reqValue.(float64))
				case reflect.String:
					reqValueStr := reqValue.(string)
					var err error
					reqValue, err = strconv.ParseInt(reqValueStr, 10, 64)
					if err != nil {
						errs = append(errs, fmt.Sprintf("invalid \"%s\" value format for %s capability", reqValueStr, reqKey))
						continue
					}
				default:
					errs = append(errs, fmt.Sprintf("invalid capability type for %s. Expected: %s, actual: %s",
						reqKey, capValueKind.String(), reqValueKind.String()))
					continue
				}
			} else if capValueKind == reflect.Bool {
				switch reqValueKind {
				case reflect.String:
					reqValueStr := reqValue.(string)
					var err error
					reqValue, err = strconv.ParseBool(reqValueStr)
					if err != nil {
						errs = append(errs, fmt.Sprintf("invalid \"%s\" value format for %s capability", reqValueStr, reqKey))
						continue
					}
				default:
					errs = append(errs, fmt.Sprintf("invalid capability type for %s. Expected: %s, actual: %s",
						reqKey, capValueKind.String(), reqValueKind.String()))
					continue
				}
			} else {
				errs = append(errs, fmt.Sprintf("invalid capability type for %s. Expected: %s, actual: %s",
					reqKey, capValueKind.String(), reqValueKind.String()))
				continue
			}

			capValue.Set(reflect.ValueOf(reqValue))
		}
	}

	var err error
	if len(errs) != 0 {
		err = errors.New(strings.Join(errs, "\n"))
	}

	return c, err
}

func (c *Capabilities) GetScreenResolution() (string, error) {
	if c.ScreenResolution == "" {
		return "1920x1080x24", nil
	}
	if fullResolutionFormat.MatchString(c.ScreenResolution) {
		return c.ScreenResolution, nil
	}
	if shortResolutionFormat.MatchString(c.ScreenResolution) {
		return fmt.Sprintf("%sx24", c.ScreenResolution), nil
	}
	return "", fmt.Errorf(
		"malformed screenResolution capability: %s. Correct format is WxH (1920x1080) or WxHxD (1920x1080x24)",
		c.ScreenResolution,
	)
}

func (c *Capabilities) GetVideoScreenSize() (string, error) {
	screenResolution, err := c.GetScreenResolution()
	if err != nil {
		return "", fmt.Errorf(
			"malformed screenResolution capability: %s. Correct format is WxH (1920x1080) or WxHxD (1920x1080x24)",
			c.ScreenResolution,
		)
	}
	return screenResolution, nil
}
