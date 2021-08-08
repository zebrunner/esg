package selenium

import (
	"github.com/zebrunner/esg/config"
	"time"
)

type ContainerConfiguration struct {
	BrowserName string
	BrowserVersion string
	PlatformName string
	Proxy map[string]interface{}
	Timeouts string
	EnableVNC bool
	EnableVideo bool
	EnableLog bool
	ScreenResolution string
	DeviceName string
	Skin string
	CpuResource int64
	MemoryResource int64
	IdleTimeout time.Duration
	VideoCodec string
	TimeZone string
	Env []string
	HostEntries []string
	DNSServers []string
}

type LegacyCaps struct {
	Name string
	Platform string
	Version string
}

func GetContainerConfiguration(capabilities map[string]interface{}) *ContainerConfiguration {
	// The method extracts capabilities used in configuring new task.

	return nil
}

func UpdateCapabilities(capabilities map[string]interface{}) map[string]interface{} {
	// The method does some capabilities changes to support legacy capabilities, fix some grid  specific behavior
	return capabilities
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

//func (c *Caps) ProcessExtensionCapabilities() {
//	if c.W3CVersion != "" {
//		c.Version = c.W3CVersion
//	}
//	if c.W3CPlatform != "" {
//		c.Platform = c.W3CPlatform
//	}
//
//	if c.ExtensionCapabilities != nil {
//		err := mergo.Merge(c, *c.ExtensionCapabilities, mergo.WithOverride) //We probably need to handle returned error
//		if err != nil {
//			return
//		}
//
//		//According to Selenium standard vendor-specific capabilities for
//		//intermediary node should not be proxied to endpoint node
//		c.ExtensionCapabilities = nil
//	}
//}
