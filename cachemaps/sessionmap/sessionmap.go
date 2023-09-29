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
	RouterUUID  string
	StartedAt   time.Time
	AccessedAt  time.Time
	IdleTimeout float64
	Network     environment.NetworkConfiguration
	TaskId      string
	Status      SessionStatus
	StopReason  StoppedReason `json:",omitempty"`
	Workspace   string
}

func CreateEntity(sessionId string, env *environment.ExecutionEnvironment, taskId *string) (*Session, error) {
	cachedTask, err := taskmap.Find(*taskId, false)
	if err != nil {
		log.WithError(err).Error("Failed to find task cache. Session not cached!")
		return nil, err
	}

	err = mapper.UpdateSessionId(env.RouterUUID, sessionId)
	if err != nil {
		log.WithError(err).Error("Session not cached!")
		return nil, err
	}

	cachedSession := &Session{
		SessionID:   sessionId,
		RouterUUID:  env.RouterUUID,
		StartedAt:   time.Now(),
		AccessedAt:  time.Now(),
		IdleTimeout: float64(env.Capabilities.IdleTimeout),
		Network:     *env.Network,
		TaskId:      cachedTask.TaskId,
		Status:      SessionActive,
		Workspace:   cachedTask.Workspace,
	}

	err = Write(cachedSession.SessionID, cachedSession, 0)
	if err != nil {
		log.WithError(err).Error("Session not cached!")
		return nil, err
	}

	cachedTask.CurrentSessionID = sessionId
	err = taskmap.Write(cachedTask.TaskId, cachedTask, -1)
	if err != nil {
		log.WithError(err).Error("Session id not cached for task!")
		return nil, err
	}

	return cachedSession, nil
}

func Find(sessionId string, rewriteAccessTime bool) (*Session, error) {
	sessionData, err := config.RedisSessionsConnection.Get(context.Background(), sessionId).Result()
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
		// -1 keeps the same ttl
		err = Write(sessionId, &session, -1)

		if err != nil {
			log.WithError(err).Error("Failed to update last access time")
		}
	}

	return &session, nil
}

func FindByRouterUUID(routerUUID string) (*Session, error) {
	sessionId, err := mapper.FindSessionId(routerUUID)
	if err != nil {
		return nil, err
	}

	return Find(*sessionId, true)
}

func Write(sessionId string, session *Session, expiration time.Duration) error {
	data, err := json.Marshal(session)
	if err != nil {
		return err
	}

	err = config.RedisSessionsConnection.Set(context.Background(), sessionId, data, expiration).Err()
	if err != nil {
		return err
	}

	if expiration > 0 {
		mapper.SetExpire(session.RouterUUID, expiration)
	}

	return nil
}

func Keys() ([]string, error) {
	return config.RedisSessionsConnection.Keys(context.Background(), "*").Result()
}
