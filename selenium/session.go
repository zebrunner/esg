package selenium

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	sessionmap "github.com/zebrunner/esg/sessinonmap"
	"github.com/zebrunner/esg/zebrunner"
)

var (
	httpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

func StartSession(ctx context.Context, driverUrl *url.URL, header http.Header, body []byte) (map[string]interface{}, error) {
	req, err := http.NewRequest(http.MethodPost, driverUrl.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	req.Host = "localhost"
	req = req.WithContext(ctx)
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}

	defer resp.Body.Close()
	var reply map[string]interface{}

	err = json.NewDecoder(resp.Body).Decode(&reply)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return reply, fmt.Errorf("unsuccessful response code. Status: %s", resp.Status)
	}

	return reply, nil
}

func CloseSession(workspace string, sessionID string, conf *config.Config) {
	sess, err := sessionmap.Find(sessionID, false)
	if err != nil {
		log.WithError(err).Error("Failed to get session from cache")
		return
	}

	sessionTime := time.Since(sess.StartedAt)

	client := http.Client{}
	sessionUrl, ok := sess.Network.GetUrl("driver")
	if ok {
		sessionUrl.Path = sessionUrl.Path + fmt.Sprintf("session/%s", sessionID)
		timeoutCtx, cancel := context.WithTimeout(context.Background(), conf.SessionDeleteTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(timeoutCtx, http.MethodDelete, sessionUrl.String(), nil)
		if err != nil {
			log.WithError(err).Error("Failed to create request")
			return
		}
		req.Host = "localhost"

		log.WithFields(log.Fields{
			"method": req.Method,
			"url":    req.URL,
		}).Debug("Closing session")
		resp, err := client.Do(req)
		if err != nil {
			log.WithError(err).Error("Failed to cancel driver session")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			log.WithField("statusCode", resp.Status).Error("Cancel request returned not success status code")
			return
		}
	} else {
		log.Warn("failed to get driver url")
	}

	if conf.ZebrunnerIsIntegrated() {
		go zebrunner.SendSessionDuration(workspace, sessionTime, conf)
	}
}
