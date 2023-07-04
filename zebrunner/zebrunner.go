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
)

const (
	USAGE_API_PATH = "/api/quota/v2/engine-usages"
	ABORT_API_PATH = "/api/reporting/api/project-test-runs/abort"
)

func TrackResourcesUsage(taskCache *taskmap.Task, d time.Duration) {
	conf := &config.Conf
	if conf.ZebrunnerHost == "" {
		// #527: don't write error message if zebrunner url is empty in the configuration
		return
	}

	l := log.WithField("_taskId", taskCache.ID)
	if taskCache.CurrentSessionID != "" {
		l = l.WithField("sessionId", taskCache.CurrentSessionID)
	}
	if !conf.SingleTenant {
		l = l.WithField("workspace", taskCache.Workspace)
	}

	requestUrl, err := url.ParseRequestURI(conf.ZebrunnerHost)
	if err != nil {
		l.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}

	platformName := strings.ToLower(taskCache.Capabilities.PlatformName)
	if platformName == "" || platformName == "generic" || platformName == "any" {
		platformName = "linux"
	}

	if !conf.SingleTenant {
		// add workspace/tenant to the url
		requestUrl.Host = taskCache.Workspace + "." + requestUrl.Host
	}
	requestUrl.Path = USAGE_API_PATH
	requestBody := map[string]interface{}{
		"cpu":      strconv.FormatInt(taskCache.Capabilities.Cpu, 10) + " millicores",
		"memory":   strconv.FormatInt(taskCache.Capabilities.Memory, 10) + " MiB",
		"instant":  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"seconds":  d.Seconds(),
		"platform": platformName,
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

func getAutomationRunId(task ecs.Task) string {
	for _, containerOverride := range task.Overrides.ContainerOverrides {
		for _, environment := range containerOverride.Environment {
			if *environment.Name == "ZEBRUNNER_LAUNCH_UUID" {
				return *environment.Value
			}
		}
	}
	return ""
}

func getStoppedReason(task ecs.Task) string {
	// get failed reason if any from any task container
	for _, container := range task.Containers {
		if container.Reason != nil {
			log.Trace(fmt.Sprintf("Container: %s; Reason: %s", *container.Name, *container.Reason))
			return *container.Reason
		}
	}
	return "Launch finished"
}

func AbortTask(taskCache *taskmap.Task, task *ecs.Task) {
	automationRunId := getAutomationRunId(*task)
	if automationRunId == "" {
		return
	}

	conf := &config.Conf

	if conf.ZebrunnerHost == "" {
		// #527: don't write error message if zebrunner url is empty in the configuration
		return
	}

	requestUrl, err := url.ParseRequestURI(fmt.Sprintf("%s%s?ciRunId=%s", conf.ZebrunnerHost, ABORT_API_PATH, automationRunId))
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}
	if !conf.SingleTenant {
		// add workspace/tenant to the url
		requestUrl.Host = taskCache.Workspace + "." + requestUrl.Host
	}

	stopReason := getStoppedReason(*task)
	requestBody := map[string]interface{}{
		"comment": stopReason,
	}

	body, err := json.Marshal(requestBody)
	if err != nil {
		log.WithError(err).Error("Failed to marshal abort data")
		return
	}
	req, err := http.NewRequest(http.MethodPost, requestUrl.String(), bytes.NewBuffer(body))
	if err != nil {
		log.WithError(err).Error("Failed to create request")
	}
	req.SetBasicAuth(conf.ZebrunnerIntegrationUser, conf.ZebrunnerIntegrationPassword)
	req.Header.Add("Content-Type", "application/json")

	log.Trace("req: ", req)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.WithError(err).Error("Failed to send request")
		return
	}

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		data := map[string]interface{}{}
		log.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Failed to abort task!")
		return
	} else {
		l := log.WithFields(log.Fields{"sessionId": taskCache.CurrentSessionID, "_taskId": taskCache.ID, "comment": stopReason})
		if !conf.SingleTenant {
			l = l.WithField("workspace", taskCache.Workspace)
		}
		l.Trace("task aborted")
	}
}
