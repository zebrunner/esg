package environment

import (
	envtype "github.com/zebrunner/esg/environment/envType"
	"github.com/zebrunner/esg/monitoring"
)

func (env *ExecutionEnvironment) configureMonitoring(workspace, esgID string) {
	if env.Type != envtype.LINUX && env.Type != envtype.PLAYWRIGHT && env.Type != envtype.GENERIC {
		return
	}
	session := monitoring.Admit()
	if session != "" {
		env.Monitoring = &monitoring.Descriptor{SessionID: session, Workspace: workspace, EsgID: esgID, EnvType: env.Type.String()}
	}
	for _, container := range env.Containers {
		if container.Name != "recorder" {
			continue
		}
		container.Env["MONITORING_ENABLED"] = "false"
		container.Env["MONITORING_MODE"] = "live"
		container.Env["MONITORING_TOKEN"] = ""
		if session != "" {
			container.Env["MONITORING_ENABLED"] = "true"
			container.Env["MONITORING_TOKEN"] = monitoring.TaskToken(workspace, esgID)
		}
	}
}
