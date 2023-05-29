package sessionmap

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

type SessionStatus int

const (
	SessionActive SessionStatus = iota
	SessionQueued
	SessionStopped
)

type StoppedReason string

const (
	SessionIdleTimeout    StoppedReason = "Session stopped due IDLE timeout"
	SessionFinished       StoppedReason = "Session finished"
	SessionAborted        StoppedReason = "Session aborted"
	SessionUnhealthy      StoppedReason = "Session aborted due to unhealthy status"
	SessionMaxTimeout     StoppedReason = "Session aborted due to the max timeout"
	SessionStartupFailure StoppedReason = "Session startup failure"
)

type Session struct {
	ID              string
	AccessedAt      time.Time
	Capabilities    capabilities.Capabilities
	RawCapabilities map[string]interface{}
	Network         environment.NetworkConfiguration
	StartedAt       time.Time
	TaskID          string
	Workspace       string
	Status          SessionStatus
	StopReason      StoppedReason `json:",omitempty"`
	UsageTracked 	bool
}

func Find(id string, rewriteAccessTime bool) (*Session, error) {
	sessionData, err := config.RedisConnection.Get(context.Background(), id).Result()
	if err != nil {
		return nil, err
	}

	var session Session
	err = json.Unmarshal([]byte(sessionData), &session)
	if err != nil {
		return nil, err
	}

	if rewriteAccessTime {
		session.AccessedAt = time.Now()
		err = Write(id, &session, 0)

		if err != nil {
			log.WithError(err).Error("Failed to update last access time")
		}
	}

	return &session, nil
}

func Write(id string, session *Session, expiration time.Duration) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	err = config.RedisConnection.Set(context.Background(), id, data, expiration).Err()
	if err != nil {
		return err
	}

	return nil
}

func Remove(id string) error {
	err := config.RedisConnection.Del(context.Background(), id).Err()
	if err != nil {
		return err
	}

	return nil
}

func Keys() ([]string, error) {
	keys, err := config.RedisConnection.Keys(context.Background(), "*").Result()

	return keys, err
}
