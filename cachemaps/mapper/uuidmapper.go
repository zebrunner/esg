package mapper

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zebrunner/esg/config"
)

type UuidMapper struct {
	UUID      string
	TaskId    string `json:",omitempty"`
	SessionID string `json:",omitempty"`
}

func InitEntity(uuid string) error {
	data, err := json.Marshal(&UuidMapper{UUID: uuid})
	if err != nil {
		return err
	}

	err = config.RedisTasksMapperConnection.Set(context.Background(), uuid, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func FindTaskId(uuid string) (*string, error) {
	mapper, err := find(uuid)
	if err != nil {
		return nil, err
	}

	return &mapper.TaskId, nil
}

func FindSessionId(uuid string) (*string, error) {
	mapper, err := find(uuid)
	if err != nil {
		return nil, err
	}

	return &mapper.SessionID, nil
}

func UpdateTaskId(uuid string, taskId string) error {
	mapper, err := find(uuid)
	if err != nil {
		return err
	}

	mapper.TaskId = taskId
	data, err := json.Marshal(mapper)
	if err != nil {
		return err
	}

	err = config.RedisTasksMapperConnection.Set(context.Background(), uuid, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func UpdateSessionId(uuid string, sessionId string) error {
	mapper, err := find(uuid)
	if err != nil {
		return err
	}

	mapper.SessionID = sessionId
	data, err := json.Marshal(mapper)
	if err != nil {
		return err
	}

	err = config.RedisTasksMapperConnection.Set(context.Background(), uuid, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func find(uuid string) (*UuidMapper, error) {
	data, err := config.RedisTasksMapperConnection.Get(context.Background(), uuid).Result()
	if err != nil {
		return nil, err
	}

	var mapper UuidMapper
	err = json.Unmarshal([]byte(data), &mapper)
	if err != nil {
		return nil, err
	}

	return &mapper, nil
}

func SetExpire(uuid string, expiration time.Duration) error {
	return config.RedisTasksMapperConnection.Expire(context.Background(), uuid, expiration).Err()
}
