package selenium

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/sessionmap"
	"github.com/zebrunner/esg/config"
)

var (
	httpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type startSessRequest struct {
	EssentialErrCh    chan error
	NonEssentialErrCh chan error
	ResponseCh        chan map[string]interface{}
}

func startSession(ctx context.Context, req *http.Request, sessReq startSessRequest) {
	req.Method = http.MethodPost
	req.Host = "localhost"
	req = req.WithContext(ctx)

	resp, err := httpClient.Do(req)
	if err != nil {
		sessReq.EssentialErrCh <- err
		return
	}

	defer resp.Body.Close()
	var reply map[string]interface{}

	err = json.NewDecoder(resp.Body).Decode(&reply)
	if err != nil {
		sessReq.EssentialErrCh <- err
		return
	}

	if resp.StatusCode != http.StatusOK {
		if resp.StatusCode == http.StatusBadRequest {
			sessReq.EssentialErrCh <- fmt.Errorf("%v", reply)
		} else {
			sessReq.NonEssentialErrCh <- fmt.Errorf("%v", reply)
		}
		return
	}

	sessReq.ResponseCh <- reply
}

func WaitForSessionStart(ctx context.Context, request *http.Request) *startSessRequest {
	sessReq := startSessRequest{
		EssentialErrCh:    make(chan error),
		NonEssentialErrCh: make(chan error),
		ResponseCh:        make(chan map[string]interface{}),
	}

	go startSession(ctx, request, sessReq)

	return &sessReq
}

func CloseSession(session *sessionmap.Session, stopReason sessionmap.StoppedReason) {
	// Set SessionStopped status and expiration time 10 minutes to be able to return sessionID and stop reason for session
	session.Status = sessionmap.SessionStopped
	session.StopReason = stopReason
	err := sessionmap.Write(session.ID, session, 10*time.Minute)
	if err != nil {
		log.WithError(err).Error("Driver session not marked as stopped!")
	}

	l := log.WithFields(log.Fields{config.TaskIdKey: session.TaskId, config.SessionIdKey: session.ID})
	if !config.Conf.SingleTenant {
		l = l.WithField("workspace", session.Workspace)
	}

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
