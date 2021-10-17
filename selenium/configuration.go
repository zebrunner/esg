package selenium

import (
	"fmt"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
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
	Cpu              int64
	Memory           int64
	IdleTimeout      int64
	VideoCodec       string
	TimeZone         string
	Env              []string
	HostsEntries     []string
	DNSServers       []string
}

func (c *ContainerConfiguration) GetMemory() int64 {
	memory := int64(config.MinMemory)
	if c.Memory > memory {
		memory = c.Memory
	}
	if memory > int64(config.MaxMemory) {
		memory = int64(config.MaxMemory)
	}
	return memory
}

func (c *ContainerConfiguration) GetCpu() int64 {
	cpu := int64(config.MinCpu)
	if c.Cpu > cpu {
		cpu = c.Cpu
	}
	if cpu > int64(config.MaxCpu) {
		cpu = int64(config.MaxCpu)
	}
	return cpu
}

func (c *ContainerConfiguration) GetTimeZone() (*time.Location, error) {
	timeZone := time.UTC
	if c.TimeZone != "" {
		tz, err := time.LoadLocation(c.TimeZone)
		if err != nil {
			log.WithError(err).WithField("value", c.TimeZone).Warn("Bad timezone specified")
		} else {
			timeZone = tz
		}
	}

	return timeZone, nil
}

type Browser struct {
	Image string
	Path  string
	Port  int64
}

func (b *Browser) TaskDefinitionFamily() string {
	parts := strings.Split(b.Image, "/")
	browser := parts[len(parts)-1]
	browser = strings.ReplaceAll(browser, ":", "-")
	browser = strings.ReplaceAll(browser, ".", "-")

	return browser
}

func (c *ContainerConfiguration) Browser() *Browser {
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

	return &Browser{
		Image: image,
		Path:  path,
		Port:  4444,
	}
}
