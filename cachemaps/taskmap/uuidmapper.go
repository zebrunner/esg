package taskmap

import (
	"context"
	"encoding/json"
	"time"

	"github.com/zebrunner/esg/config"
)

type UuidMapper struct {
	UUID   string
	TaskId string `json:",omitempty"`
}

func findTaskId(uuid string) (*string, error) {
	data, err := config.RedisTasksMapperConnection.Get(context.Background(), uuid).Result()
	if err != nil {
		return nil, err
	}

	var mapper UuidMapper
	err = json.Unmarshal([]byte(data), &mapper)
	if err != nil {
		return nil, err
	}

	return &mapper.TaskId, nil
}

func write(id string, entity *UuidMapper, expiration time.Duration) error {
	data, err := json.Marshal(entity)
	if err != nil {
		return err
	}

	err = config.RedisTasksMapperConnection.Set(context.Background(), id, data, expiration).Err()
	if err != nil {
		return err
	}

	return nil
}
