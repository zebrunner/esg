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
	uuidMapper := WriteItme{
		Mapper:     IdMapper{RouterUUID: routerUUID},
		Expiration: 10 * time.Minute,
	}

	responseCh, errCh := WriteMapper(routerUUID, uuidMapper)
	select {
	case err := <-errCh:
		return err
	case <-responseCh:
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

func SetExpireForSeveralRecords(routerUUIDs []string, expiration time.Duration) error {
	rdbPipe := config.RedisIdMapperClient.Pipeline()
	for _, routerUUID := range routerUUIDs {
		rdbPipe.Expire(context.Background(), routerUUID, expiration)
	}

	_, err := rdbPipe.Exec(context.Background())
	return err
}
