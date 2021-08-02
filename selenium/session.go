package selenium

import (
	"net/url"
	"sync"
	"time"

	"github.com/imdario/mergo"
)

// Caps - user capabilities
type Caps struct {
	Name                  string            `json:"browserName,omitempty"`
	DeviceName            string            `json:"deviceName,omitempty"`
	Version               string            `json:"version,omitempty"`
	W3CVersion            string            `json:"browserVersion,omitempty"`
	Platform              string            `json:"platform,omitempty"`
	W3CPlatform           string            `json:"platformName,omitempty"`
	ScreenResolution      string            `json:"screenResolution,omitempty"`
	Skin                  string            `json:"skin,omitempty"`
	VNC                   bool              `json:"enableVNC,omitempty"`
	Video                 bool              `json:"enableVideo,omitempty"`
	Log                   bool              `json:"enableLog,omitempty"`
	VideoScreenSize       string            `json:"videoScreenSize,omitempty"`
	VideoFrameRate        uint16            `json:"videoFrameRate,omitempty"`
	VideoCodec            string            `json:"videoCodec,omitempty"`
	LogName               string            `json:"logName,omitempty"`
	TestName              string            `json:"name,omitempty"`
	TimeZone              string            `json:"timeZone,omitempty"`
	ContainerHostname     string            `json:"containerHostname,omitempty"`
	Env                   []string          `json:"env,omitempty"`
	ApplicationContainers []string          `json:"applicationContainers,omitempty"`
	HostsEntries          []string          `json:"hostsEntries,omitempty"`
	DNSServers            []string          `json:"dnsServers,omitempty"`
	Labels                map[string]string `json:"labels,omitempty"`
	SessionTimeout        string            `json:"sessionTimeout,omitempty"`
	S3KeyPattern          string            `json:"s3KeyPattern,omitempty"`
	ExtensionCapabilities *Caps             `json:"selenoid:options,omitempty"`
	Memory                string            `json:"Memory,omitempty"`
	MemoryReservation     string            `json:"MemoryReservation,omitempty"`
	Cpu                   string            `json:"Cpu,omitempty"`
	IdleTimeout           int               `json:"idleTimeout,omitempty"`
}

func (c *Caps) ProcessExtensionCapabilities() {
	if c.W3CVersion != "" {
		c.Version = c.W3CVersion
	}
	if c.W3CPlatform != "" {
		c.Platform = c.W3CPlatform
	}

	if c.ExtensionCapabilities != nil {
		err := mergo.Merge(c, *c.ExtensionCapabilities, mergo.WithOverride) //We probably need to handle returned error
		if err != nil {
			return
		}

		//According to Selenium standard vendor-specific capabilities for
		//intermediary node should not be proxied to endpoint node
		c.ExtensionCapabilities = nil
	}
}

func (c *Caps) BrowserName() string {
	browserName := c.Name
	if browserName != "" {
		return browserName
	}
	return c.DeviceName
}

// Container - container information
type Container struct {
	ID                  string `json:"id"`
	ContainerInstanceID string
	IPAddress           string `json:"ip"`
	PublicIPAddress     string
	Ports               map[string]string `json:"exposedPorts,omitempty"`
}

// Session - holds session info
type Session struct {
	Quota     string
	Caps      Caps
	URL       *url.URL
	Container *Container
	HostPort  HostPort
	Cancel    func()
	Started   time.Time
	Lock      sync.Mutex
	TaskID    string
	Workspace string
}

// HostPort - hold host-port values for all forwarded ports
type HostPort struct {
	Selenium   string
	Fileserver string
	Clipboard  string
	VNC        string
	Devtools   string
}

// Metadata - session metadata saved to file
type Metadata struct {
	ID           string    `json:"id"`
	Capabilities Caps      `json:"capabilities"`
	Started      time.Time `json:"started"`
	Finished     time.Time `json:"finished"`
}
