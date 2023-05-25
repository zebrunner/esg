package selenium

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	//	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"

	sessionmap "github.com/zebrunner/esg/sessinonmap"
	//	"github.com/zebrunner/esg/zebrunner"
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

func CloseSession(session *sessionmap.Session) {
    l := log.WithFields(log.Fields{"_taskId": session.TaskID, "sessionId": session.ID, "workspace": session.Workspace})

	conf := &config.Conf
	client := http.Client{}
	sessionUrl, ok := session.Network.GetUrl("driver")
	if ok {
		sessionUrl.Path = sessionUrl.Path + fmt.Sprintf("session/%s", session.ID)
		timeoutCtx, cancel := context.WithTimeout(context.Background(), conf.SessionDeleteTimeout)
		defer cancel()
		req, err := http.NewRequestWithContext(timeoutCtx, http.MethodDelete, sessionUrl.String(), nil)
		if err != nil {
			l.WithError(err).Error("Failed to create request")
			return
		}
		req.Host = "localhost"

		l.WithFields(log.Fields{"method": req.Method, "url": req.URL}).Debug("closing driver")
		resp, err := client.Do(req)
		if err != nil {
			l.WithError(err).Error("Failed to close driver")
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			l.WithField("statusCode", resp.Status).Error("Cancel request returned not success status code")
			return
		}
	} else {
		l.Error("failed to get driver url")
	}

	l.Debug("driver closed")
}
