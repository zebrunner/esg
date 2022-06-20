package zebrunner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"math"
	"net/http"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

const (
	DURATION_API_PATH = "/api/quota/v1/org-metrics/%s/alterations/engine-execution-time"
)

func SendSessionDuration(workspace string, d time.Duration, conf *config.Config) {
	resource := fmt.Sprintf(DURATION_API_PATH, workspace)

	requestUrl, err := url.ParseRequestURI(conf.ZebrunnerHost)
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}
	requestUrl.Host = workspace + "." + requestUrl.Host
	requestUrl.Path = resource
	requestBody := map[string]interface{}{
		"instant": time.Now().UTC().Format("2006-01-02T15:04:05Z"),
		"minutes": int(math.Ceil(d.Seconds() / float64(time.Second))),
	}

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

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.WithError(err).Error("Failed to send request")
		return
	}

	if resp.StatusCode != http.StatusNoContent {
		data := map[string]interface{}{}
		err = json.NewDecoder(resp.Body).Decode(&data)

		bodystr, _ := ioutil.ReadAll(resp.Body)
		log.WithField("body", string(bodystr)).Info("Response body")
		if err != nil {
			log.WithError(err).Error("Failed to send session duration to zebrunner")
		}
		log.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Response got unsuccessfull code")
		return
	}
}
