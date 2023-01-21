package zebrunner

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"time"
        "strconv"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

        sessionmap "github.com/zebrunner/esg/sessinonmap"
)

const (
        USAGE_API_PATH = "/api/quota/v2/engine-usages"
)

func TrackResourcesUsage(sess *sessionmap.Session, d time.Duration) {
	conf := &config.Conf
	requestUrl, err := url.ParseRequestURI(conf.ZebrunnerHost)
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}
	requestUrl.Host = sess.Workspace + "." + requestUrl.Host
	requestUrl.Path = USAGE_API_PATH
	requestBody := map[string]interface{}{
                "cpu": strconv.FormatInt(sess.Capabilities.Cpu, 10) + " millicores",
                "memory": strconv.FormatInt(sess.Capabilities.Memory, 10) + " MiB",
		"instant": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"seconds": d.Seconds(),
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

		//bodystr, _ := ioutil.ReadAll(resp.Body)
		if err != nil {
			log.WithError(err).Error("Failed to track task resource usage")
		}
		log.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Response got unsuccessfull code")
		return
	} else {
		log.WithField("_taskId", sess.ID).WithField("workspace", sess.Workspace).WithField("request body", requestBody).Info("shape recorded")
	}
}
