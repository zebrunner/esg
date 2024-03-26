package new

import (
	"context"
	"encoding/json"
	"fmt"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment"
	"time"
)

type Status int

const (
	None Status = iota
	Queued
	Cypress
	Active
	Stopped
)

type StoppedReason string

const (
	TaskStartupFailure StoppedReason = "task startup failure"
	TaskUnhealthy      StoppedReason = "task aborted due to unhealthy status"
	TaskMaxTimeout     StoppedReason = "task aborted due to the max timeout"
	TaskLost           StoppedReason = "task aborted as it wasn't found in cache"
	TaskAborted        StoppedReason = "task aborted"
	TaskFinished       StoppedReason = "task finished"
	// Implement?
	SessiongStartupFailure StoppedReason = "healthy task failed to start session"
	SessionIdleTimeout     StoppedReason = "session stopped due IDLE timeout"
)

type Mapper struct {
	RouterUUID    string
	Capabilities  *capabilities.Capabilities
	Network       environment.NetworkConfiguration
	IdleTimeout   float64
	TaskStatus    Status
	SessionStatus Status
	UsageTracked  bool
	HealthAt      *time.Time
	AccessedAt    *time.Time
	TaskId        string        `json:",omitempty"`
	SessionID     string        `json:",omitempty"`
	StopReason    StoppedReason `json:",omitempty"`
	Workspace     string        `json:",omitempty"`
}

func CreateEntity(env *environment.ExecutionEnvironment) (*Mapper, error) {
	creationTime := time.Now()
	m := &Mapper{
		RouterUUID:    env.RouterUUID,
		Capabilities:  env.Capabilities,
		Network:       *env.Network,
		IdleTimeout:   float64(env.Capabilities.IdleTimeout),
		TaskStatus:    Queued,
		SessionStatus: None,
		UsageTracked:  false,
		HealthAt:      &creationTime,
		AccessedAt:    &creationTime,
	}

	return m, nil
}

func Write(mapper *Mapper, expiration time.Duration) error {
	if mapper.RouterUUID == "" {
		return fmt.Errorf("uuid can't be empty")
	}

	data, err := json.Marshal(mapper)
	if err != nil {
		return err
	}

	err = config.RedisMapperClient.Set(context.Background(), mapper.RouterUUID, data, expiration).Err()
	if err != nil {
		return err
	}

	return nil
}

func Find(uuid string, rewriteAccessTime bool) (*Mapper, error) {
	data, err := config.RedisMapperClient.Get(context.Background(), uuid).Result()
	if err != nil {
		return nil, err
	}

	var entity Mapper
	err = json.Unmarshal([]byte(data), &entity)
	if err != nil {
		return nil, err
	}

	if rewriteAccessTime {
		curTime := time.Now()
		entity.AccessedAt = &curTime
		// -1 keeps the same ttl
		err = Write(&entity, -1)
		if err != nil {
			log.WithError(err).Error("Failed to update last access time")
		}
	}

	return &entity, nil
}
