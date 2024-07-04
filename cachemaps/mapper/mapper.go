package mapper

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/capabilities"
	"github.com/zebrunner/esg/config"
	"github.com/zebrunner/esg/environment/network"
)

type Status int

const (
	Queued Status = iota
	// Delete after https://github.com/zebrunner/entrypoint/issues/85 is resolved
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
	SessionIdleTimeout StoppedReason = "session stopped due IDLE timeout"
)

type Mapper struct {
	RouterUUID   string
	Capabilities *capabilities.Capabilities
	Network      network.NetworkConfiguration
	IdleTimeout  float64
	Status       Status
	UsageTracked bool
	HealthAt     *time.Time
	AccessedAt   *time.Time
	TaskId       string        `json:",omitempty"`
	SessionID    string        `json:",omitempty"`
	StopReason   StoppedReason `json:",omitempty"`
	Workspace    string        `json:",omitempty"`
}

func (m Mapper) IsIdle() bool {
	if m.Status != Active {
		return false
	}

	idleTime := time.Since(*m.AccessedAt).Seconds()
	return idleTime > m.IdleTimeout
}

func CreateEntity(workspace string, routerUUID string, caps *capabilities.Capabilities, netConf *network.NetworkConfiguration, expiration time.Duration) (*Mapper, error) {
	creationTime := time.Now()
	m := &Mapper{
		RouterUUID:   routerUUID,
		Capabilities: caps,
		Network:      *netConf,
		IdleTimeout:  float64(caps.IdleTimeout),
		Status:       Queued,
		UsageTracked: false,
		HealthAt:     &creationTime,
		AccessedAt:   &creationTime,
		Workspace:    workspace,
	}

	err := WritedByWorker(m, nil, nil, expiration)

	return m, err
}

func Write(mapper *Mapper, expiration time.Duration) error {
	if mapper.RouterUUID == "" {
		return fmt.Errorf("uuid can't be empty")
	}

	data, err := json.Marshal(mapper)
	if err != nil {
		return err
	}

	err = config.RedisCluster.Set(context.Background(), mapper.RouterUUID, data, expiration).Err()
	if err != nil {
		return err
	}

	return nil
}

func Find(uuid string) (*Mapper, error) {
	data, err := config.RedisCluster.Get(context.Background(), uuid).Result()
	if err != nil {
		return nil, err
	}

	var entity Mapper
	err = json.Unmarshal([]byte(data), &entity)
	if err != nil {
		return nil, err
	}

	return &entity, nil
}

func WriteShapedEntities(mappers map[string]Mapper, expiration time.Duration) error {
	return cachemaps.WriteWithExpire(config.RedisCluster.Pipeline(), cachemaps.TASK, mappers, expiration)
}

func FindAll(uuids []string) ([]Mapper, error) {
	return cachemaps.FindAll[Mapper](config.RedisCluster.Pipeline(), uuids)
}
