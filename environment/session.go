package environment

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-redis/redis/v8"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/selenium"
	"github.com/zebrunner/esg/utils"
	"github.com/zebrunner/esg/zebrunner"
)

var (
	httpClient = &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

// Session - holds session info
type Session struct {
	ID              string
	Capabilities    selenium.Capabilities
	RawCapabilities map[string]interface{}
	Network         NetworkConfiguration
	Cancel          func()
	StartedAt       time.Time
	TaskID          string
	Workspace       string
}

type CachedSession struct {
	ID              string
	Capabilities    selenium.Capabilities
	RawCapabilities map[string]interface{}
	Network         NetworkConfiguration
	Timeout         time.Duration
	StartedAt       time.Time
	TaskID          string
	Workspace       string
}

func (s CachedSession) MarshalBinary() (data []byte, err error) {
	bytes, err := json.Marshal(s)
	return bytes, err
}

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

func CreateSessionFromCache(sessionID string) (*Session, error) {
	result, err := config.RedisConnection.Get(context.Background(), sessionID).Result()
	if err == redis.Nil {
		return nil, &utils.SeleniumError{
			ResponseStatus: http.StatusNotFound,
			SeleniumCode:   "invalid session id",
			Message:        fmt.Sprintf("Session with id %s not found in active sessions.", sessionID),
			Err:            err,
		}
	}
	if err != nil {
		return nil, err
	}
	s := CachedSession{}
	err = json.Unmarshal([]byte(result), &s)
	if err != nil {
		return nil, err
	}

	seleniumSession := Session{
		ID:              s.ID,
		RawCapabilities: s.RawCapabilities,
		Network:         s.Network,
		StartedAt:       s.StartedAt,
		TaskID:          s.TaskID,
		Workspace:       s.Workspace,
	}
	seleniumSession.Cancel = func() {}
	return &seleniumSession, nil
}

func SaveSessionToCache(session *Session) error {
	cacheSession := CachedSession{
		ID:              session.ID,
		RawCapabilities: session.RawCapabilities,
		Network:         session.Network,
		StartedAt:       session.StartedAt,
		TaskID:          session.TaskID,
		Workspace:       session.Workspace,
	}
	err := config.RedisConnection.Set(context.Background(), session.ID, cacheSession, 0).Err()
	if err != nil {
		return err
	}

	keyTimeout := time.Duration(session.Capabilities.IdleTimeout*int64(time.Second) + int64(10*time.Minute))
	err = config.RedisConnection.Set(context.Background(), session.ID+"-timeout", session.Capabilities.IdleTimeout, keyTimeout).Err()
	return err
}

func CloseSession(workspace string, sessionID string, conf *config.Config) {
	sess, err := CreateSessionFromCache(sessionID)
	if err != nil {
		log.WithError(err).Error("Failed to get session from cache")
		return
	}
	defer sess.Cancel()

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
		go zebrunner.SendSessionDuration(workspace, time.Since(sess.StartedAt), conf)
	}
	_, err = config.RedisConnection.Del(context.Background(), sessionID).Result()
	if err != nil {
		log.WithError(err).Error("Failed to delete session from redis")
		return
	}
	log.WithField("sessionID", sessionID).Info("Session closed.")
}
