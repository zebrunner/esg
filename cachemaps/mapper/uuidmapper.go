package mapper

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zebrunner/esg/config"
)

type IdMapper struct {
	RouterUUID string
	TaskId     string `json:",omitempty"`
	SessionID  string `json:",omitempty"`
}

func InitEntity(routerUUID string) error {
	data, err := json.Marshal(&IdMapper{RouterUUID: routerUUID})
	if err != nil {
		return err
	}

	err = config.RedisIdMapperClient.Set(context.Background(), routerUUID, data, config.Conf.ServiceStartupTimeout).Err()
	if err != nil {
		return err
	}

	return nil
}

// only internal usage (use methods from sessionmap/taskmap)
func FindTaskId(routerUUID string) (*string, error) {
	mapper, err := find(routerUUID)
	if err != nil {
		return nil, err
	}

	return &mapper.TaskId, nil
}

// only internal usage (use methods from sessionmap/taskmap)
func FindSessionId(routerUUID string) (*string, error) {
	mapper, err := find(routerUUID)
	if err != nil {
		return nil, err
	}

	return &mapper.SessionID, nil
}

// only internal usage (use methods from sessionmap/taskmap)
func UpdateTaskId(routerUUID string, taskId string) error {
	mapper, err := find(routerUUID)
	if err != nil {
		return err
	}

	mapper.TaskId = taskId
	data, err := json.Marshal(mapper)
	if err != nil {
		return err
	}

	err = config.RedisIdMapperClient.Set(context.Background(), routerUUID, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

// only internal usage (use methods from sessionmap/taskmap)
func UpdateSessionId(routerUUID string, sessionId string) error {
	mapper, err := find(routerUUID)
	if err != nil {
		return err
	}

	mapper.SessionID = sessionId
	data, err := json.Marshal(mapper)
	if err != nil {
		return err
	}

	err = config.RedisIdMapperClient.Set(context.Background(), routerUUID, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func find(routerUUID string) (*IdMapper, error) {
	data, err := config.RedisIdMapperClient.Get(context.Background(), routerUUID).Result()
	if err != nil {
		return nil, err
	}

	var mapper IdMapper
	err = json.Unmarshal([]byte(data), &mapper)
	if err != nil {
		return nil, err
	}

	return &mapper, nil
}

// only internal usage (use methods from sessionmap/taskmap)
func SetExpire(routerUUID string, expiration time.Duration) error {
	return config.RedisIdMapperClient.Expire(context.Background(), routerUUID, expiration).Err()
}
