package zebrunner

import (
	"bytes"
	"encoding/json"
	"github.com/aws/aws-sdk-go/service/ecs"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"fmt"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

	sessionmap "github.com/zebrunner/esg/sessinonmap"
)

const (
        USAGE_API_PATH = "/api/quota/v2/engine-usages"
	ABORT_API_PATH = "/api/reporting/api/project-test-runs/abort"
)

func TrackResourcesUsage(sess *sessionmap.Session, d time.Duration) {
	conf := &config.Conf
	requestUrl, err := url.ParseRequestURI(conf.ZebrunnerHost)
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}

	platformName := strings.ToLower(sess.Capabilities.PlatformName)
	if platformName==""|| platformName == "generic" {
		platformName = "linux"
	}


	requestUrl.Host = sess.Workspace + "." + requestUrl.Host
	requestUrl.Path = USAGE_API_PATH
	requestBody := map[string]interface{}{
                "cpu": strconv.FormatInt(sess.Capabilities.Cpu, 10) + " millicores",
                "memory": strconv.FormatInt(sess.Capabilities.Memory, 10) + " MiB",
		"instant": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"seconds": d.Seconds(),
		"platform":  platformName,
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
		log.WithField("_taskId", sess.ID).WithField("workspace", sess.Workspace).WithField("request body", requestBody).Info("shape recorded")
	}
}

func getAutomationRunId(task ecs.Task) string {
	for _, containerOverride:= range task.Overrides.ContainerOverrides {
		for _, environment:=range containerOverride.Environment {
			if *environment.Name == "ZEBRUNNER_LAUNCH_UUID" {
				return *environment.Value
			}
		}
	}
	return ""
}

func AbortTask(sess *sessionmap.Session, task *ecs.Task) {
        automationRunId := getAutomationRunId(*task)
        if automationRunId ==""{
                log.Debug("Automation Launch Id is not available.")
                return
        }

	conf := &config.Conf

        // #479: register quesry arg to avoid encoding of "?" mark
	requestUrl, err := url.ParseRequestURI(fmt.Sprintf("%s%s?ciRunId=%s", conf.ZebrunnerHost, ABORT_API_PATH, automationRunId))
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}
	requestUrl.Host = sess.Workspace + "." + requestUrl.Host

	requestBody := map[string]interface{}{
		"comment": "Launch finished",
	}
	log.Trace("request body to abort call: ", requestBody)

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

	if resp.StatusCode != http.StatusNoContent {
		data := map[string]interface{}{}
                log.WithFields(log.Fields{
                        "status":   resp.Status,
                        "response": data,
                }).Error("Failed to abort task!")

		return
	} else {
		log.WithField("_taskId", sess.ID).WithField("workspace", sess.Workspace).WithField("request body", requestBody).Info("task aborted")
	}
}
