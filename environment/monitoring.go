package environment

import (
	"github.com/zebrunner/esg/config"
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/monitoring"
)

func (env *ExecutionEnvironment) configureMonitoring(workspace, esgID string) {
	if env.Type != envtype.LINUX && env.Type != envtype.PLAYWRIGHT && env.Type != envtype.GENERIC {
		return
	}
	for _, container := range env.Containers {
		if container.Name != "recorder" {
			continue
		}
		container.Env["ROUTER_UUID"] = esgID
		container.Env["MONITORING_ENABLED"] = "false"
		container.Env["MONITORING_MODE"] = "live"
		container.Env["MONITORING_TOKEN"] = ""
		container.Env["MONITORING_ENV_TYPE"] = ""
		if config.Conf.MonitoringEnabled && len(config.Conf.MonitoringIntegrationToken) >= 32 {
			container.Env["MONITORING_ENABLED"] = "true"
			container.Env["MONITORING_MODE"] = "summary"
			container.Env["MONITORING_TOKEN"] = monitoring.TaskToken(workspace, esgID)
			container.Env["MONITORING_ENV_TYPE"] = env.Type.String()
		}
	}
}
