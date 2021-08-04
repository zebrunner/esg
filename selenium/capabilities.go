package selenium

import "github.com/zebrunner/esg/config"

type StandardCaps struct {
	BrowserName               string                 `json:"browserName,omitempty"`
	BrowserVersion            string                 `json:"browserVersion,omitempty"`
	PlatformName              string                 `json:"platformName,omitempty"`
	AcceptInsecureCerts       bool                   `json:"acceptInsecureCerts,omitempty"`
	PageLoadStrategy          string                 `json:"pageLoadStrategy,omitempty"`
	Proxy                     map[string]interface{} `json:"proxy,omitempty"`
	SetWindowRect             bool                   `json:"setWindowRect,omitempty"`
	Timeouts                  map[string]interface{} `json:"timeouts,omitempty"`
	StrictFileInteractability bool                   `json:"strictFileInteractability,omitempty"`
	UnhandledPromptBehavior   string                 `json:"unhandledPromptBehavior,omitempty"`
}

type LegacyCaps struct {
	Name     string `json:"name,omitempty"`
	Platform string `json:"platform,omitempty"`
	Version  string `json:"version,omitempty"`
}

type ExtensionCaps struct {
	EnableVNC   bool `json:"enableVNC,omitempty"`
	EnableVideo bool `json:"enableVideo,omitempty"`
	EnableLog   bool `json:"enableLog,omitempty"`

	//TODO
	ScreenResolution string `json:"screenResolution,omitempty"`
	// Use screen resolution
	VideoScreenSize string `json:"videoScreenSize,omitempty"`

	// ????
	DeviceName string `json:"deviceName,omitempty"`
	Skin       string `json:"skin,omitempty"`

	MemoryResource int64 `json:"memory,omitempty"`
	CpuResource    int64 `json:"cpu,omitempty"`
}

// platform - legacy
// browser - legacy

// Caps - user capabilities
//type Caps struct {
//	//Name                  string            `json:"browserName,omitempty"`
//	//legacy
//	//Version string `json:"version,omitempty"`
//	//W3CVersion            string            `json:"browserVersion,omitempty"`
//	//legacy
//	//Platform string `json:"platform,omitempty"`
//	//W3CPlatform           string            `json:"platformName,omitempty"`
//	//todo
//	ScreenResolution string `json:"screenResolution,omitempty"`
//	//??
//	Skin string `json:"skin,omitempty"`
//
//	EnableVNC   bool `json:"enableVNC,omitempty"`
//	EnableVideo bool `json:"enableVideo,omitempty"`
//	EnableLog   bool `json:"enableLog,omitempty"`
//
//	// Use screen resolution
//	VideoScreenSize string `json:"videoScreenSize,omitempty"`
//	//todo
//	VideoFrameRate uint16 `json:"videoFrameRate,omitempty"`
//	//todo
//	VideoCodec string `json:"videoCodec,omitempty"`
//	//delete
//	LogName string `json:"logName,omitempty"`
//	//delete
//	TestName string `json:"name,omitempty"`
//	//todo
//	TimeZone string `json:"timeZone,omitempty"`
//	//delete
//	ContainerHostname string `json:"containerHostname,omitempty"`
//	//todo
//	Env []string `json:"env,omitempty"`
//	//delete
//	ApplicationContainers []string `json:"applicationContainers,omitempty"`
//	//todo
//	HostsEntries []string `json:"hostsEntries,omitempty"`
//	//todo
//	DNSServers []string `json:"dnsServers,omitempty"`
//	//delete
//	Labels map[string]string `json:"labels,omitempty"`
//	//it is idle timeout
//	SessionTimeout string `json:"sessionTimeout,omitempty"`
//	//delete
//	S3KeyPattern string `json:"s3KeyPattern,omitempty"`
//	//to zebrunner: support both
//	ExtensionCapabilities *Caps  `json:"selenoid:options,omitempty"`
//	Memory                string `json:"Memory,omitempty"`
//	//delete (review ecs.go)
//	MemoryReservation string `json:"MemoryReservation,omitempty"`
//	Cpu               string `json:"Cpu,omitempty"`
//	//same sa session timeout
//	IdleTimeout int `json:"idleTimeout,omitempty"`
//}

type Caps struct {
	StandardCaps
	LegacyCaps
	ExtensionCaps
}

func (c *Caps) Memory() int64 {
	memory := int64(config.MinMemory)
	if c.MemoryResource > memory {
		memory = c.MemoryResource
	}
	if memory > int64(config.MaxMemory) {
		memory = int64(config.MaxMemory)
	}
	return memory
}

func (c *Caps) Cpu() int64 {
	cpu := int64(config.MinCpu)
	if c.CpuResource > cpu {
		cpu = c.CpuResource
	}
	if cpu > int64(config.MaxCpu) {
		cpu = int64(config.MaxCpu)
	}
	return cpu
}

type RequestCaps struct {
	DesiredCapabilities Caps `json:"desiredCapabilities"`
	Capabilities        struct {
		AlwaysMatch Caps    `json:"alwaysMatch"`
		FirstMatch  []*Caps `json:"firstMatch"`
	} `json:"capabilities"`
}

func (rc *RequestCaps) ProcessRequestCaps() (Caps, error) {
	// Method for processing and validation requested capabilities following W3C algorithm
	// https://www.w3.org/TR/webdriver/#processing-capabilities
	// Method should also validate capabilities and return validation error if some fields are invalid
	requiredCapabilites := rc.Capabilities.FirstMatch
	fmCapabilities := rc.



	return caps, nil
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

func (c *Caps) BrowserName() string {
	browserName := c.Name
	if browserName != "" {
		return browserName
	}
	return c.DeviceName
}
