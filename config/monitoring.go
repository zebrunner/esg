package config

import log "github.com/sirupsen/logrus"

func (c *Config) ValidateMonitoring() {
	if c.MonitoringEnabled && len(c.MonitoringIntegrationToken) < 32 {
		c.MonitoringEnabled = false
		log.Warn("Disabled task monitoring: integration token must contain at least 32 bytes")
	}
}
