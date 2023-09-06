package mapper

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zebrunner/esg/config"
)

type UuidMapper struct {
	RouterUUID string
	TaskId     string `json:",omitempty"`
	SessionID  string `json:",omitempty"`
}

func InitEntity(routerUUID string) error {
	data, err := json.Marshal(&UuidMapper{RouterUUID: routerUUID})
	if err != nil {
		return err
	}

	err = config.RedisTasksMapperConnection.Set(context.Background(), routerUUID, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func FindTaskId(routerUUID string) (*string, error) {
	mapper, err := find(routerUUID)
	if err != nil {
		return nil, err
	}

	return &mapper.TaskId, nil
}

func FindSessionId(routerUUID string) (*string, error) {
	mapper, err := find(routerUUID)
	if err != nil {
		return nil, err
	}

	return &mapper.SessionID, nil
}

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

	err = config.RedisTasksMapperConnection.Set(context.Background(), routerUUID, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

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

	err = config.RedisTasksMapperConnection.Set(context.Background(), routerUUID, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func find(routerUUID string) (*UuidMapper, error) {
	data, err := config.RedisTasksMapperConnection.Get(context.Background(), routerUUID).Result()
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

func SetExpire(routerUUID string, expiration time.Duration) error {
	return config.RedisTasksMapperConnection.Expire(context.Background(), routerUUID, expiration).Err()
}
