package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

func SendSessionDuration(workspace string, d time.Duration) {
	resource := fmt.Sprintf("/api/quota/v1/org-metrics/%s/alterations/engine-execution-time", workspace)

	requestUrl, err := url.ParseRequestURI(config.ZebrunnerHost)
	if err != nil {
		log.WithError(err).Error("Failed to parse zebrunner base url")
		return
	}
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
	req.SetBasicAuth(config.ZebrunnerIntegrationUser, config.ZebrunnerIntegrationPassword)
	req.Header.Add("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		log.WithError(err).Error("Failed to send request")
		return
	}

	if resp.StatusCode != http.StatusNoContent {
		data := map[string]interface{}{}
		json.NewDecoder(resp.Body).Decode(&data)
		log.WithFields(log.Fields{
			"status":   resp.Status,
			"response": data,
		}).Error("Response got unsuccessfull code")
		return
	}
}
