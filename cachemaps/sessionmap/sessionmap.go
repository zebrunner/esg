package sessionmap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps/mapper"
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

// Creates a new record in sessions redis db. Updates mapper's record with taskId, sessionId and resets expiration
func CreateEntity(sessionId string, env *environment.ExecutionEnvironment, taskId *string) (*Session, error) {
	err := mapper.WriteMapperRecord(mapper.IdMapper{RouterUUID: env.RouterUUID, TaskId: *taskId, SessionID: sessionId}, 0)
	if err != nil {
		log.WithField(config.SessionIdKey, sessionId).WithError(err).Error("Session not cached!")
		return nil, err
	}

	cachedSession := &Session{
		SessionID:   sessionId,
		RouterUUID:  env.RouterUUID,
		StartedAt:   time.Now(),
		AccessedAt:  time.Now(),
		IdleTimeout: float64(env.Capabilities.IdleTimeout),
		Network:     *env.Network,
		TaskId:      *taskId,
		Status:      SessionActive,
		Workspace:   env.Workspace,
	}

	err = WriteSession(*cachedSession, 0)
	if err != nil {
		log.WithField(config.SessionIdKey, sessionId).WithError(err).Error("Session not cached!")
		return nil, err
	}

	return cachedSession, nil
}

func Find(sessionId string, rewriteAccessTime bool) (*Session, error) {
	sessionData, err := config.RedisSessionsClient.Get(context.Background(), sessionId).Result()
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

	err = config.RedisSessionsClient.Set(context.Background(), sessionId, data, expiration).Err()
	if err != nil {
		return err
	}

	return nil
}

// Returns all sessions from redis
func Sessions() ([]Session, error) {
	keys, err := config.RedisSessionsClient.Keys(context.Background(), "*").Result()
	if err != nil {
		return nil, err
	}

	rdbPipe := config.RedisSessionsClient.Pipeline()
	for _, key := range keys {
		rdbPipe.Get(context.Background(), key)
	}

	cmds, err := rdbPipe.Exec(context.Background())
	if err != nil {
		return nil, err
	}

	sessions := make([]Session, 0)
	for _, cmd := range cmds {
		data, err := cmd.(*redis.StringCmd).Result()
		if err != nil {
			log.WithError(err).Warn("Failed to get cached session")
			continue
		}

		var session Session
		err = json.Unmarshal([]byte(data), &session)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}

	return sessions, nil
}
