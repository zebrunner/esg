package mapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
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
	// Children lists the session ids issued by browser refreshes, in the order they were handed out.
	Children []string `json:",omitempty"`
	// RootUUID marks this record as a pointer to the session that owns the task, and is set on it alone.
	RootUUID string `json:",omitempty"`
	// RequestedUUID is the id the caller looked up and is never persisted, so it cannot shadow RouterUUID.
	RequestedUUID string `json:"-"`
}

// CurrentUUID reports the id the caller addressed, which is a child session id after a browser refresh.
func (m Mapper) CurrentUUID() string {
	if m.RequestedUUID != "" {
		return m.RequestedUUID
	}

	return m.RouterUUID
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

// UpdateSession applies a change to the newest session record and retries concurrent changes.
// The update function can run more than once, so it must be idempotent.
func UpdateSession(routerUUID string, update func(*Mapper) error) error {
	const maxAttempts = 5

	ctx := context.Background()
	for attempt := 0; attempt < maxAttempts; attempt++ {
		err := config.RedisCluster.Watch(ctx, func(tx *redis.Tx) error {
			data, err := tx.Get(ctx, routerUUID).Bytes()
			if err != nil {
				return err
			}

			var entity Mapper
			if err := json.Unmarshal(data, &entity); err != nil {
				return err
			}
			if err := update(&entity); err != nil {
				return err
			}

			data, err = json.Marshal(&entity)
			if err != nil {
				return err
			}

			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, routerUUID, data, redis.KeepTTL)
				return nil
			})
			return err
		}, routerUUID)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}

		return err
	}

	return fmt.Errorf("failed to update session after %d concurrent changes", maxAttempts)
}

// UpdateAccessedAt updates only a current session record and preserves concurrent state changes.
// The returned value is false when the session is already stopped.
func UpdateAccessedAt(routerUUID string, accessedAt time.Time) (bool, error) {
	errSessionStopped := errors.New("session is stopped")
	err := UpdateSession(routerUUID, func(entity *Mapper) error {
		if entity.Status == Stopped {
			return errSessionStopped
		}
		entity.AccessedAt = &accessedAt
		return nil
	})
	if errors.Is(err, errSessionStopped) {
		return false, nil
	}

	return err == nil, err
}

// Find returns the session that owns uuid, following a child session id to the record holding the task.
func Find(uuid string) (*Mapper, error) {
	entity, err := findExact(uuid)
	if err != nil {
		return nil, err
	}

	if entity.RootUUID != "" {
		entity, err = findExact(entity.RootUUID)
		if err != nil {
			return nil, err
		}
	}

	entity.RequestedUUID = uuid

	return entity, nil
}

// WriteChild records a new session id that resolves to the session owning the task.
func WriteChild(childUUID string, rootUUID string, expiration time.Duration) error {
	if childUUID == "" || rootUUID == "" {
		return fmt.Errorf("uuid can't be empty")
	}

	// Stored as a bare pointer so the record cannot be mistaken for a half-populated session.
	data, err := json.Marshal(struct {
		RouterUUID string
		RootUUID   string
	}{RouterUUID: childUUID, RootUUID: rootUUID})
	if err != nil {
		return err
	}

	return config.RedisCluster.Set(context.Background(), childUUID, data, expiration).Err()
}

// ExpireChildren puts child session ids on the same countdown as the session they resolve to.
func ExpireChildren(children []string, expiration time.Duration) error {
	if len(children) == 0 {
		return nil
	}

	pipe := config.RedisCluster.Pipeline()
	for _, child := range children {
		pipe.Expire(context.Background(), child, expiration)
	}

	_, err := pipe.Exec(context.Background())

	return err
}

func findExact(uuid string) (*Mapper, error) {
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
