package config

import log "github.com/sirupsen/logrus"

func (c *Config) ValidateMonitoring() {
	if c.MonitoringCompatibilityEnabled && len(c.MonitoringIntegrationToken) < 32 {
		c.MonitoringCompatibilityEnabled = false
		log.Warn("Disabled monitoring compatibility: integration token must contain at least 32 bytes")
	}
}
