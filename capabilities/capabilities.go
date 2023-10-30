package capabilities

import (
	"fmt"
	"strconv"

	"regexp"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	fullResolutionFormat  = regexp.MustCompile(`^([0-9]+x[0-9]+)x(8|16|24)$`)
	shortResolutionFormat = regexp.MustCompile(`^[0-9]+x[0-9]+$`)
	shortFromFullFormat   = regexp.MustCompile(`^[0-9]+x[0-9]+`)

	// 40x30
	minScrenResolution = []string{"40", "30"}
	// max aspect ratio 1:6 or 6:1
	maxScreenAspectRation = 6
	// added to deal with hardcoded recorder and uploader cpu/memory usage. Also should deal with the limited time for video upload after test
	// default was: 1920x1080=2_073_600. Should be tested with max possible resolution and framerate, as max pixels value is increased by 50%.
	maxVideoResolutionPixels = 3_000_000
	// if frame rate higher than 60, video speed will increase accordingly
	maxVideoFrameRate int64 = 30
)

func formatError(value interface{}, cap string, capType string) string {
	return fmt.Sprintf("invalid format for %s capability. Cannot parse \"%v\" into field of type %s", cap, value, capType)
}

func typeError(value interface{}, cap string, capType string) string {
	return fmt.Sprintf("invalid type for %s capability. Cannot assign \"%v\" to field of type %s", cap, value, capType)
}

func malformedError(value interface{}, cap string, msg string) error {
	return fmt.Errorf("malformed %s capability: %v. %s", cap, value, msg)
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

	if value == nil {
		i.From(0)
	} else if valueFloat, ok := value.(float64); ok {
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

	//Video related caps
	EnableVideo     boolWrapper
	VideoScreenSize stringWrapper
	VideoCodec      stringWrapper
	FrameRate       int64Wrapper

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
	LaunchUUID    stringWrapper
}

func (c *Capabilities) GetTimeZone() (*time.Location, error) {
	if c.TimeZone == "" {
		return time.UTC, nil
	}
	tz, err := time.LoadLocation(c.TimeZone.ToPrimitive())
	if err != nil {
		return nil, malformedError(c.TimeZone, "timeZone", "Bad timezone specified")
	}
	return tz, nil
}

func (c *Capabilities) GetScreenResolution() (string, error) {
	var resolution string
	if fullResolutionFormat.MatchString(c.ScreenResolution.ToPrimitive()) {
		resolution = c.ScreenResolution.ToPrimitive()
	} else if shortResolutionFormat.MatchString(c.ScreenResolution.ToPrimitive()) {
		resolution = fmt.Sprintf("%sx24", c.ScreenResolution)
	} else {
		return "", malformedError(c.ScreenResolution, "screenResolution", "Correct format is WxH (1920x1080) or WxHxD (1920x1080x24)")
	}

	err := validateScreenResolution(resolution)
	if err != nil {
		return "", malformedError(c.ScreenResolution, "screenResolution", err.Error())
	}

	return resolution, nil
}

func validateScreenResolution(resolution string) error {
	resArrInt := make([]int, 0)
	for _, v := range strings.Split(resolution, "x") {
		resInt, _ := strconv.Atoi(v)
		resArrInt = append(resArrInt, resInt)
	}

	minResArrInt := make([]int, 0)
	for _, v := range minScrenResolution {
		resInt, _ := strconv.Atoi(v)
		minResArrInt = append(minResArrInt, resInt)
	}

	for i := 0; i < len(minResArrInt); i++ {
		if minResArrInt[i] > resArrInt[i] {
			return fmt.Errorf("min resolution is %s", strings.Join(minScrenResolution, "x"))
		}
	}

	if float64(resArrInt[0])/float64(resArrInt[1]) > float64(maxScreenAspectRation) ||
		float64(resArrInt[1])/float64(resArrInt[0]) > float64(maxScreenAspectRation) {
		return fmt.Errorf("max aspect ratio is 1:%[1]v or %[1]v:1", maxScreenAspectRation)
	}

	return nil
}

// recorder container uses only short resolution format
func (c *Capabilities) GetVideoScreenSize(screenResolution string) (string, error) {
	if c.VideoScreenSize == "" {
		return shortFromFullFormat.FindString(screenResolution), nil
	}

	var videoScreenSize string
	if fullResolutionFormat.MatchString(c.VideoScreenSize.ToPrimitive()) {
		videoScreenSize = shortFromFullFormat.FindString(c.VideoScreenSize.ToPrimitive())
	} else if shortResolutionFormat.MatchString(c.VideoScreenSize.ToPrimitive()) {
		videoScreenSize = c.VideoScreenSize.ToPrimitive()
	} else {
		return "", malformedError(c.VideoScreenSize, "videoScreenSize", "correct format is WxH (1920x1080) or WxHxD (1920x1080x24)")
	}

	err := validateVideoResolution(videoScreenSize, screenResolution)
	if err != nil {
		return "", malformedError(c.VideoScreenSize, "videoScreenSize", err.Error())
	}

	return videoScreenSize, nil
}

func validateVideoResolution(videoScreenSize, screenResolution string) error {
	resArrInt := make([]int, 0)
	for _, v := range strings.Split(screenResolution, "x") {
		resInt, _ := strconv.Atoi(v)
		resArrInt = append(resArrInt, resInt)
	}

	videoResArrInt := make([]int, 0)
	for _, v := range strings.Split(videoScreenSize, "x") {
		resInt, _ := strconv.Atoi(v)
		videoResArrInt = append(videoResArrInt, resInt)
	}

	for i := 0; i < len(videoResArrInt); i++ {
		if videoResArrInt[i] > resArrInt[i] {
			return fmt.Errorf("video resolution should not be higher than screen resolution")
		}
	}

	if videoResArrInt[0]*videoResArrInt[1] > maxVideoResolutionPixels {
		return fmt.Errorf("video max total pixels should not be higher than %v", maxVideoResolutionPixels)
	}

	return nil
}

func (c *Capabilities) GetFrameRate() (string, error) {
	if c.FrameRate <= 0 || c.FrameRate.ToPrimitive() > maxVideoFrameRate {
		return "", malformedError(c.FrameRate, "frameRate", fmt.Sprintf("Correct value should be in range [1:%v]", maxVideoFrameRate))
	}

	return strconv.FormatInt(c.FrameRate.ToPrimitive(), 10), nil
}

func (c *Capabilities) GetIdleTimeout() float64 {
	maxIdleTimeout := config.Conf.MaxIdleTimeout.Seconds()
	idleTimeout := float64(c.IdleTimeout.ToPrimitive())
	if idleTimeout > maxIdleTimeout {
		log.WithFields(log.Fields{"idleTimeout": idleTimeout, "maxIdleTimeout": maxIdleTimeout}).
			Warn("IdleTimeout time exceeds the maximum allowed value. IdleTimeout capability is set to permitted maximum")
		c.IdleTimeout = int64Wrapper(maxIdleTimeout)
	}

	return float64(c.IdleTimeout.ToPrimitive())
}

func GetDefaultCaps() *Capabilities {
	// set default values, that are differ from default primitive values (like int64 == 0, bool == false, etc)
	return &Capabilities{
		EnableVNC:        true,
		EnableLog:        true,
		ScreenResolution: "1920x1080x24",

		IdleTimeout: int64Wrapper(config.Conf.IdleTimeout.Seconds()),
		MaxTimeout:  int64Wrapper(config.Conf.MaxTimeout.Seconds()),

		EnableVideo: true,
		FrameRate:   12,
		VideoCodec:  "libx264",

		EnvVariables: make(mapStrStrWrapper, 0),
	}
}

func (c *Capabilities) ParseRequestCaps(reqCaps map[string]interface{}) error {
	mapping := map[string]Validator{
		"browsername":      &c.BrowserName,
		"browserversion":   &c.BrowserVersion,
		"platformname":     &c.PlatformName,
		"platformversion":  &c.PlatformVersion,
		"proxy":            &c.Proxy,
		"timeouts":         &c.Timeouts,
		"enablevnc":        &c.EnableVNC,
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

		"enablevideo":     &c.EnableVideo,
		"videoscreensize": &c.VideoScreenSize,
		"videocodec":      &c.VideoCodec,
		"framerate":       &c.FrameRate,

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
		err = fmt.Errorf(strings.Join(errs, "\n"))
	} else {
		if launchUUID, ok := c.EnvVariables["ZEBRUNNER_LAUNCH_UUID"]; ok {
			c.LaunchUUID.From(launchUUID)
		}

		c.GetIdleTimeout()
	}

	return err
}

type capsForPlatform func(executor string, version string) ([]*Capabilities, error)

func capsForAndroid(executor string, version string) ([]*Capabilities, error) {
	capsList := make([]*Capabilities, 0)
	caps := GetDefaultCaps()
	reqCaps := map[string]interface{}{
		"platformName":    "android",
		"deviceName":      executor,
		"platformVersion": version,
	}

	err := caps.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	return append(capsList, caps), nil
}

func capsForWindows(executor string, version string) ([]*Capabilities, error) {
	capsList := make([]*Capabilities, 0)
	reqCaps := map[string]interface{}{
		"platformName":   "windows",
		"browserName":    executor,
		"browserVersion": version,
	}
	capsWithoutMitm := GetDefaultCaps()
	err := capsWithoutMitm.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	capsList = append(capsList, capsWithoutMitm)
	return capsList, nil
}

func capsForLinux(executor string, version string) ([]*Capabilities, error) {
	capsList := make([]*Capabilities, 0)
	reqCaps := map[string]interface{}{
		"platformName":   "linux",
		"browserName":    executor,
		"browserVersion": version,
	}

	capsWithoutMitm := GetDefaultCaps()
	err := capsWithoutMitm.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	capsList = append(capsList, capsWithoutMitm)

	reqCaps["mitm"] = true

	capsWithMitm := GetDefaultCaps()
	err = capsWithMitm.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	capsList = append(capsList, capsWithMitm)
	return capsList, nil
}

func capsForCypress(executor string, version string) ([]*Capabilities, error) {
	capsList := make([]*Capabilities, 0)
	caps := GetDefaultCaps()
	reqCaps := map[string]interface{}{
		"platformName":   "cypress",
		"browserName":    executor,
		"browserVersion": version,
	}

	err := caps.ParseRequestCaps(reqCaps)
	if err != nil {
		return nil, err
	}

	return append(capsList, caps), nil
}

func FromImage(image string) ([]*Capabilities, error) {
	executors := map[string]capsForPlatform{
		"redroid": capsForAndroid,

		"chrome":  capsForLinux,
		"firefox": capsForLinux,
		"edge":    capsForLinux,

		"windows-chrome": capsForWindows,

		"cypress-chrome":   capsForCypress,
		"cypress-chromium": capsForCypress,
		"cypress-edge":     capsForCypress,
		"cypress-firefox":  capsForCypress,
	}

	parts := strings.Split(image, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("failed to parse image, image is in invalid format. image=%s", image)
	}

	executor := parts[0]
	version := parts[1]

	getCapsFn, ok := executors[executor]
	if !ok {
		return nil, fmt.Errorf("failed to build capabilities from unknown image. image=%s", image)
	}

	capsList, err := getCapsFn(executor, version)
	if err != nil {
		return nil, err
	}

	return capsList, nil
}
