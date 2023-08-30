package zebrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/service/ecs"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

const (
	USAGE_API_PATH = "/api/engine-utilization/v1/engine-usages"
	ABORT_API_PATH = "/api/reporting/api/project-test-runs/abort"
)

func TrackResourcesUsage(cachedTask *taskmap.Task, task *ecs.Task) {
	//log.Info("Task:", task)
	conf := &config.Conf
	if conf.ZebrunnerHost == "" {
		// #527: don't write error message if zebrunner url is empty in the configuration
		return
	}

	l := log.WithField(config.RouterUuid, cachedTask.UUID).WithField(config.TaskIdKey, cachedTask.TaskId)
	if cachedTask.CurrentSessionID != "" {
		l = l.WithField(config.SessionIdKey, cachedTask.CurrentSessionID)
	}

	requestUrl, err := url.ParseRequestURI(conf.ZebrunnerHost)
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}

	if task.StartedAt == nil || task.StoppingAt == nil {
		// don't calculate timing for terminated tasks by AWS due to the missted StartedAt!
		//      StopCode: \"TerminationNotice\"
		//      StoppedReason: \"Host EC2 (instance i-03dba81187d65ce7e) terminated.\"
		if task.StartedAt == nil {
			l = l.WithField("StartedAt", task.StartedAt) // nil
		} else {
			l = l.WithField("StartedAt", *task.StartedAt) //time
		}

		if task.StoppingAt == nil {
			l = l.WithField("StartedAt", task.StoppingAt) // nil
		} else {
			l = l.WithField("StartedAt", *task.StoppingAt) //time
		}

		l.Warn("Unable to track resourse usage!")
		return
	}

	l.Trace("StartedAt: ", *task.StartedAt)
	l.Trace("StoppingAt: ", *task.StoppingAt)

	l.Trace("HealthAt: ", cachedTask.HealthAt)
	startedAt := *task.StartedAt //local var needed to calculate difference via Sub(..)
	stoppingAt := *task.StoppingAt

	duration := stoppingAt.Sub(startedAt)
	healthAt := cachedTask.HealthAt

	provisioningTime := healthAt.Sub(startedAt) //diff between healthAt and startedAt provide task preparation time
	l.Trace("provisioningSeconds: ", provisioningTime.Seconds())

	platformName := strings.ToLower(cachedTask.Capabilities.PlatformName.ToPrimitive())
	if platformName == "" || platformName == "generic" || platformName == "any" {
		platformName = "linux"
	}

	cpuUsage := cachedTask.Capabilities.Cpu.ToPrimitive()
	memUsage := cachedTask.Capabilities.Memory.ToPrimitive()
	if cachedTask.Capabilities.Mitm {
		// #579: track mitm container cpu and memory usage
		cpuUsage += cachedTask.Capabilities.MitmCpu.ToPrimitive()
		memUsage += cachedTask.Capabilities.MitmMemory.ToPrimitive()
	}

	if !conf.SingleTenant {
		// add workspace/tenant to the url
		requestUrl.Host = cachedTask.Workspace + "." + requestUrl.Host
		l = l.WithField("workspace", cachedTask.Workspace)
	}
	requestUrl.Path = USAGE_API_PATH
	requestBody := map[string]interface{}{
		"cpu":       strconv.FormatInt(cpuUsage, 10) + " millicores",
		"memory":    strconv.FormatInt(memUsage, 10) + " MiB",
		"instant":   time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"seconds":   duration.Seconds() - provisioningTime.Seconds(), // register only net time without provisioning time
		"platform":  platformName,
		"taskId":    cachedTask.UUID,
		"sessionId": cachedTask.CurrentSessionID,
	}
	l.Trace("request body to track resources: ", requestBody)

	body, err := json.Marshal(requestBody)
	if err != nil {
		l.WithError(err).Error("Failed to marshal request data")
		return
	}
	req, err := http.NewRequest(http.MethodPost, requestUrl.String(), bytes.NewBuffer(body))
	if err != nil {
		l.WithError(err).Error("Failed to create request")
	}
	req.SetBasicAuth(conf.ZebrunnerIntegrationUser, conf.ZebrunnerIntegrationPassword)
	req.Header.Add("Content-Type", "application/json")
	l.Trace("req: ", req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.WithError(err).Error("Failed to send request")
		return
	}

	if resp.StatusCode != http.StatusNoContent {
		data := map[string]interface{}{}
		err = json.NewDecoder(resp.Body).Decode(&data)

		if err != nil {
			l.WithError(err).Error("Failed to track task resource usage")
		}
		l.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Failed to track task resource usage!")
		return
	} else {
		l.WithField("request body", requestBody).Info("shape recorded")
	}
}

func AbortTask(env *environment.ExecutionEnvironment, reason string) {
	conf := &config.Conf

	if conf.ZebrunnerHost == "" {
		// #527: don't write error message if zebrunner url is empty in the configuration
		return
	}

	l := log.WithFields(log.Fields{config.RouterUuid: env.UUID, "comment": reason})

	requestUrl, err := url.ParseRequestURI(
		fmt.Sprintf("%s%s?ciRunId=%s", conf.ZebrunnerHost, ABORT_API_PATH, env.Capabilities.LaunchUUID.ToPrimitive()))
	if err != nil {
		l.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}

	if !conf.SingleTenant {
		l = l.WithField("workspace", env.Workspace)
		requestUrl.Host = env.Workspace + "." + requestUrl.Host
	}

	// stopReason := getStoppedReason(*task)
	requestBody := map[string]interface{}{
		"comment": reason,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		l.WithError(err).Error("Failed to marshal abort data")
		return
	}
	req, err := http.NewRequest(http.MethodPost, requestUrl.String(), bytes.NewBuffer(body))
	if err != nil {
		l.WithError(err).Error("Failed to create request")
	}
	req.SetBasicAuth(conf.ZebrunnerIntegrationUser, conf.ZebrunnerIntegrationPassword)
	req.Header.Add("Content-Type", "application/json")

	l.Trace("req: ", req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		l.WithError(err).Error("Failed to send request")
		return
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		data := map[string]interface{}{}
		l.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Failed to abort launch!")
	} else {
		l.Debug("launch aborted")
	}
}
