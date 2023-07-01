package zebrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/aws/aws-sdk-go/service/ecs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

	sessionmap "github.com/zebrunner/esg/sessinonmap"
)

const (
	USAGE_API_PATH = "/api/quota/v2/engine-usages"
	ABORT_API_PATH = "/api/reporting/api/project-test-runs/abort"
)

func TrackResourcesUsage(sess *sessionmap.Session, task *ecs.Task) {
	//log.Info("Task:", task)
	conf := &config.Conf
	if conf.ZebrunnerHost == "" {
		// #527: don't write error message if zebrunner url is empty in the configuration
		return
	}
	requestUrl, err := url.ParseRequestURI(conf.ZebrunnerHost)
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}

	l := log.WithFields(log.Fields{"sessionId": sess.ID, "_taskId": sess.TaskID})
	if task.StartedAt == nil || task.StoppingAt == nil {
		// don't calculate timing for terminated tasks by AWS due to the missted StartedAt!
		//      StopCode: \"TerminationNotice\"
		//      StoppedReason: \"Host EC2 (instance i-03dba81187d65ce7e) terminated.\"
		l.WithFields(log.Fields{"StartedAt": *task.StartedAt, "StoppingAt": *task.StoppingAt}).Warn("Unable to track resourse usage!")
		return
	}

	l.Trace("StartedAt: ", *task.StartedAt)
	l.Trace("StoppingAt: ", *task.StoppingAt)

	l.Trace("HealthAt: ", sess.HealthAt)
	startedAt := *task.StartedAt //local var needed to calculate difference via Sub(..)
	stoppingAt := *task.StoppingAt

	duration := stoppingAt.Sub(startedAt)
	healthAt := sess.HealthAt

	healthSeconds := healthAt.Sub(startedAt) //diff between healthAt and startedAt provide task preparation time
	l.Trace("healthSeconds: ", healthSeconds.Seconds())

	platformName := strings.ToLower(sess.Capabilities.PlatformName)
	if platformName == "" || platformName == "generic" || platformName == "any" {
		platformName = "linux"
	}

	if !conf.SingleTenant {
		// add workspace/tenant to the url
		requestUrl.Host = sess.Workspace + "." + requestUrl.Host
	}
	requestUrl.Path = USAGE_API_PATH
	requestBody := map[string]interface{}{
		"cpu":      strconv.FormatInt(sess.Capabilities.Cpu, 10) + " millicores",
		"memory":   strconv.FormatInt(sess.Capabilities.Memory, 10) + " MiB",
		"instant":  time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"seconds":  duration.Seconds() - healthSeconds.Seconds(), // register only net time without preparation steps
		"platform": platformName,
	}
	log.Trace("request body to track resources: ", requestBody)

	body, err := json.Marshal(requestBody)
	if err != nil {
		log.WithError(err).Error("Failed to marshal request data")
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

	if resp.StatusCode != http.StatusNoContent {
		data := map[string]interface{}{}
		err = json.NewDecoder(resp.Body).Decode(&data)

		if err != nil {
			log.WithError(err).Error("Failed to track task resource usage")
		}
		log.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Failed to track task resource usage!")
		return
	} else {
		l := log.WithFields(log.Fields{"sessionId": sess.ID, "_taskId": sess.TaskID, "request body": requestBody})
		if !conf.SingleTenant {
			l = l.WithField("workspace", sess.Workspace)
		}
		l.Info("shape recorded")
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

func AbortTask(sess *sessionmap.Session, task *ecs.Task) {
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
		requestUrl.Host = sess.Workspace + "." + requestUrl.Host
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
		l := log.WithFields(log.Fields{"sessionId": sess.ID, "_taskId": sess.TaskID, "comment": stopReason})
		if !conf.SingleTenant {
			l = l.WithField("workspace", sess.Workspace)
		}
		l.Trace("task aborted")
	}
}
