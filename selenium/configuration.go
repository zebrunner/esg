package selenium

import (
	"fmt"
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

