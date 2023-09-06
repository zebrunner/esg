package sessionmap

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
	"github.com/zebrunner/esg/cachemaps/taskmap"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
)

type SessionStatus int

const (
	SessionActive SessionStatus = iota
	SessionStopped
)

type StoppedReason string

const (
	SessionIdleTimeout StoppedReason = "session stopped due IDLE timeout"
	SessionMaxTimeout  StoppedReason = "session aborted due to the max timeout"
	SessionAborted     StoppedReason = "session aborted"
	SessionFinished    StoppedReason = "session finished"
)

type Session struct {
	SessionID   string
	UUID        string
	StartedAt   time.Time
	AccessedAt  time.Time
	IdleTimeout float64
	Network     environment.NetworkConfiguration
	TaskId      string
	Status      SessionStatus
	StopReason  StoppedReason `json:",omitempty"`
	Workspace   string
}

func CreateEntity(sessionId string, env *environment.ExecutionEnvironment, task *taskmap.Task) (*Session, error) {
	err := mapper.UpdateSessionId(env.UUID, sessionId)
	if err != nil {
		log.WithError(err).Error("Session not cached!")
		return nil, err
	}

	cachedSession := &Session{
		SessionID:   sessionId,
		UUID:        env.UUID,
		StartedAt:   time.Now(),
		AccessedAt:  time.Now(),
		IdleTimeout: float64(env.Capabilities.IdleTimeout),
		Network:     *env.Network,
		TaskId:      task.TaskId,
		Status:      SessionActive,
		Workspace:   task.Workspace,
	}

	err = Write(cachedSession.SessionID, cachedSession, 0)
	if err != nil {
		log.WithError(err).Error("Session not cached!")
		return nil, err
	}

	task.CurrentSessionID = sessionId
	err = taskmap.Write(task.TaskId, task, 0)
	if err != nil {
		log.WithError(err).Error("Session id not cached for task!")
		return nil, err
	}

	return cachedSession, nil
}

func Find(id string, rewriteAccessTime bool) (*Session, error) {
	sessionData, err := config.RedisSessionsConnection.Get(context.Background(), id).Result()
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

func FindByUuid(uuid string) (*Session, error) {
	sessionId, err := mapper.FindSessionId(uuid)
	if err != nil {
		return nil, err
	}

	return Find(*sessionId, true)
}

func Write(id string, session *Session, expiration time.Duration) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	err = config.RedisSessionsConnection.Set(context.Background(), id, data, expiration).Err()
	if err != nil {
		return err
	}

	if expiration > 0 {
		mapper.SetExpire(session.UUID, expiration)
	}

	return nil
}

func Keys() ([]string, error) {
	return config.RedisSessionsConnection.Keys(context.Background(), "*").Result()
}
