package capabilities

import (
	"errors"
	"fmt"
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

func formatError(value interface{}, cap string, capType string) string {
	return fmt.Sprintf("invalid format for %s capability. Cannot parse \"%v\" into field of type %s", cap, value, capType)
}

func typeError(value interface{}, cap string, capType string) string {
	return fmt.Sprintf("invalid type for %s capability. Cannot assign \"%v\" to field of type %s", cap, value, capType)
}

type Validator interface {
	Validate(string, any) string
}

type Wrapper[T any] interface {
	ToPrimitive() T
	From(T)
}

type stringWrapper string

func (s *stringWrapper) Validate(key string, value interface{}) string {
	errStr := ""
	if valueStr, ok := value.(string); ok {
		s.From(valueStr)
	} else {
		errStr = typeError(value, key, "string")
	}

	return errStr
}

func (s *stringWrapper) ToPrimitive() string {
	return string(*s)
}

func (s *stringWrapper) From(value string) {
	*s = stringWrapper(value)
}

type boolWrapper bool

func (b *boolWrapper) Validate(key string, value interface{}) string {
	errStr := ""

	if valueBool, ok := value.(bool); ok {
		b.From(valueBool)
	} else if valueStr, ok := value.(string); ok {
		if valueBool, err := strconv.ParseBool(valueStr); err == nil {
			b.From(valueBool)
		} else {
			errStr = formatError(value, key, "bool")
		}
	} else {
		errStr = typeError(value, key, "bool")
	}

	return errStr
}

func (b *boolWrapper) ToPrimitive() bool {
	return bool(*b)
}

func (b *boolWrapper) From(value bool) {
	*b = boolWrapper(value)
}

type int64Wrapper int64

func (i *int64Wrapper) Validate(key string, value interface{}) string {
	errStr := ""

	if valueFloat, ok := value.(float64); ok {
		i.From(int64(valueFloat))
	} else if valueStr, ok := value.(string); ok {
		if valueInt, err := strconv.ParseInt(valueStr, 10, 64); err == nil {
			i.From(valueInt)
		} else {
			errStr = formatError(value, key, "int")
		}
	} else {
		errStr = typeError(value, key, "int")
	}

	return errStr
}

func (i *int64Wrapper) ToPrimitive() int64 {
	return int64(*i)
}

func (i *int64Wrapper) From(value int64) {
	*i = int64Wrapper(value)
}

type sliceStringWrapper []string

func (sliceStr *sliceStringWrapper) Validate(key string, value interface{}) string {
	errStr := ""
	if valueStrSlice, ok := value.([]string); ok {
		sliceStr.From(valueStrSlice)
	} else if valueSlice, ok := value.([]interface{}); ok {
		reqSliceStr := make([]string, len(valueSlice))
		for _, v := range valueSlice {
			if vStr, ok := v.(string); ok {
				reqSliceStr = append(reqSliceStr, vStr)
			} else {
				errStr = typeError(v, key, "[]string")
				break
			}
		}
		sliceStr.From(reqSliceStr)
	} else {
		errStr = typeError(value, key, "[]string")
	}

	return errStr
}

func (sliceStr *sliceStringWrapper) ToPrimitive() []string {
	return []string(*sliceStr)
}

func (sliceStr *sliceStringWrapper) From(value []string) {
	*sliceStr = sliceStringWrapper(value)
}

type mapStrInterfaceWrapper map[string]interface{}

func (m *mapStrInterfaceWrapper) Validate(key string, value interface{}) string {
	errStr := ""

	if valueMap, ok := value.(map[string]interface{}); ok {
		m.From(valueMap)
	} else {
		errStr = typeError(value, key, "map[string]interface{}")
	}

	return errStr
}

func (m *mapStrInterfaceWrapper) ToPrimitive() map[string]interface{} {
	return map[string]interface{}(*m)
}

func (m *mapStrInterfaceWrapper) From(value map[string]interface{}) {
	*m = mapStrInterfaceWrapper(value)
}

type mapStrStrWrapper map[string]string

func (m *mapStrStrWrapper) Validate(key string, value interface{}) string {
	errStr := ""

	if valueMapStr, ok := value.(map[string]string); ok {
		m.From(valueMapStr)
	} else if valueMap, ok := value.(map[string]interface{}); ok {
		valueMapStr := make(map[string]string, len(valueMap))
		for k, v := range valueMap {
			if vStr, ok := v.(string); ok {
				valueMapStr[k] = vStr
			} else {
				errStr = typeError(fmt.Sprintf("%s:%v", k, v), key, "map[string]string")
				break
			}
		}
		m.From(valueMapStr)
	} else {
		errStr = typeError(value, key, "map[string]string")
	}

	return errStr
}

func (m *mapStrStrWrapper) ToPrimitive() map[string]string {
	return map[string]string(*m)
}

func (m *mapStrStrWrapper) From(value map[string]string) {
	*m = mapStrStrWrapper(value)
}

type Capabilities struct {
	BrowserName      stringWrapper
	BrowserVersion   stringWrapper
	PlatformName     stringWrapper
	PlatformVersion  stringWrapper
	Proxy            mapStrInterfaceWrapper
	Timeouts         stringWrapper
	EnableVNC        boolWrapper
	EnableVideo      boolWrapper
	EnableLog        boolWrapper
	EnableDebug      boolWrapper
	ScreenResolution stringWrapper
	DeviceName       stringWrapper
	IdleTimeout      int64Wrapper
	MaxTimeout       int64Wrapper
	TimeZone         stringWrapper
	Env              sliceStringWrapper
	HostsEntries     sliceStringWrapper
	DNSServers       sliceStringWrapper

	//Vendor caps
	Cpu    int64Wrapper
	Memory int64Wrapper
	//Mitm proxy caps
	Mitm       boolWrapper   //enabl mitm with har dump and output generation for mitmweb
	MitmArgs   stringWrapper // list of arguments for mitmdump command. Important: --verbose and --quiet will be appended forcibly
	MitmCpu    int64Wrapper
	MitmMemory int64Wrapper

	// generic launcher caps
	RepositoryUrl stringWrapper
	Branch        stringWrapper
	Image         stringWrapper
	LaunchCommand stringWrapper
	EnvVariables  mapStrStrWrapper
}

func (c *Capabilities) GetTimeZone() (*time.Location, error) {
	timeZone := time.UTC
	if c.TimeZone != "" {
		tz, err := time.LoadLocation(c.TimeZone.ToPrimitive())
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
			PlatformVersion: stringWrapper(version),
		})
	} else if in(executor, platforms["linux"]) {
		capsList = append(capsList, &Capabilities{
			PlatformName:   "linux",
			BrowserName:    stringWrapper(executor),
			BrowserVersion: stringWrapper(version),
			Mitm:           false,
		})

		capsList = append(capsList, &Capabilities{
			PlatformName:   "linux",
			BrowserName:    stringWrapper(executor),
			BrowserVersion: stringWrapper(version),
			Mitm:           true,
		})
	} else if in(executor, platforms["cypress"]) {
		capsList = append(capsList, &Capabilities{
			PlatformName:   "cypress",
			BrowserName:    stringWrapper(executor),
			BrowserVersion: stringWrapper(version),
		})
	} else {
		return nil, fmt.Errorf("failed to build capabilities from unknown image. image=%s", image)
	}

	return capsList, nil
}
func FromRequestCaps(reqCaps map[string]interface{}) (*Capabilities, error) {
	c := &Capabilities{}
	mapping := map[string]Validator{
		"browsername":      &c.BrowserName,
		"browserversion":   &c.BrowserVersion,
		"platformname":     &c.PlatformName,
		"platformversion":  &c.PlatformVersion,
		"proxy":            &c.Proxy,
		"timeouts":         &c.Timeouts,
		"enablevnc":        &c.EnableVNC,
		"enablevideo":      &c.EnableVideo,
		"enablelog":        &c.EnableLog,
		"enabledebug":      &c.EnableDebug,
		"screenresolution": &c.ScreenResolution,
		"devicename":       &c.DeviceName,
		"idletimeout":      &c.IdleTimeout,
		"maxtimeout":       &c.MaxTimeout,
		"timezone":         &c.TimeZone,
		"env":              &c.Env,
		"hostsentries":     &c.HostsEntries,
		"dnsservers":       &c.DNSServers,

		"cpu":    &c.Cpu,
		"memory": &c.Memory,

		"mitm":       &c.Mitm,
		"mitmargs":   &c.MitmArgs,
		"mitmcpu":    &c.MitmCpu,
		"mitmmemory": &c.MitmMemory,

		"repositoryurl": &c.RepositoryUrl,
		"branch":        &c.Branch,
		"image":         &c.Image,
		"launchcommand": &c.LaunchCommand,
		"envvariables":  &c.EnvVariables,
	}

	errs := make([]string, 0)
	for key, value := range reqCaps {
		keyLower := strings.ToLower(key)
		if validator := mapping[keyLower]; validator != nil {
			errStr := validator.Validate(key, value)
			if errStr != "" {
				errs = append(errs, errStr)
			}
		}
	}

	var err error
	if len(errs) > 0 {
		err = errors.New(strings.Join(errs, "\n"))
	}

	return c, err
}

func (c *Capabilities) GetScreenResolution() (string, error) {
	if c.ScreenResolution == "" {
		return "1920x1080x24", nil
	}
	if fullResolutionFormat.MatchString(c.ScreenResolution.ToPrimitive()) {
		return c.ScreenResolution.ToPrimitive(), nil
	}
	if shortResolutionFormat.MatchString(c.ScreenResolution.ToPrimitive()) {
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

func (c *Capabilities) GenerateError(businessError string, internalError error) error {
	if c.EnableDebug {
		return fmt.Errorf("%s; [debug] %s", businessError, internalError.Error())
	} else {
		return fmt.Errorf("%s", businessError)
	}
}
