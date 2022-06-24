package capabilities

import (
	"fmt"
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
	Skin             string
	IdleTimeout      int64
	VideoCodec       string
	timeZone         string
	Env              []string
	HostsEntries     []string
	DNSServers       []string

	Cpu               int64
	Memory            int64
	MemoryReservation int64
}

func (c *Capabilities) TimeZone() (*time.Location, error) {
	timeZone := time.UTC
	if c.timeZone != "" {
		tz, err := time.LoadLocation(c.timeZone)
		if err != nil {
			log.WithError(err).WithField("value", c.TimeZone).Warn("Bad timezone specified")
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

func FromImage(image string) (*Capabilities, error) {
	platforms := map[string][]string{
		"android": {"redroid"},
		"linux":   {"chrome", "firefox", "edge"},
	}

	parts := strings.Split(image, ":")
	if len(parts) != 2 {
		return nil, fmt.Errorf("failed to parse image, image is in invalid format. image=%s", image)
	}

	executor := parts[0]
	version := parts[1]
	if executor == "redroid" {
		return &Capabilities{
			PlatformName:    "android",
			DeviceName:      "redroid",
			PlatformVersion: version,
		}, nil
	} else if in(executor, platforms["linux"]) {
		return &Capabilities{
			PlatformName:   "linux",
			BrowserName:    executor,
			BrowserVersion: version,
		}, nil
	} else {
		return nil, fmt.Errorf("failed to build capabilities from unknown image. image=%s", image)
	}
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
