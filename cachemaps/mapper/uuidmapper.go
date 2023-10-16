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

// Create new record with ServiceStartupTimeout timeout, that contains only router uuid.
func InitEntity(routerUUID string) error {
	responseCh, errCh := WriteMapper(IdMapper{RouterUUID: routerUUID}, config.Conf.ServiceStartupTimeout)
	select {
	case err := <-errCh:
		return err
	case <-responseCh:
		return nil
	}
}

// Only internal usage (use methods from sessionmap/taskmap)
func FindTaskId(routerUUID string) (*string, error) {
	mapper, err := find(routerUUID)
	if err != nil {
		return nil, err
	}

	return &mapper.TaskId, nil
}

// Only internal usage (use methods from sessionmap/taskmap)
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

// Updates expiration time for passed record ids.
// Expiration value of <= 0 deletes the record immediately.
func SetExpireForSeveralRecords(routerUUIDs []string, expiration time.Duration) error {
	rdbPipe := config.RedisIdMapperClient.Pipeline()
	for _, routerUUID := range routerUUIDs {
		rdbPipe.Expire(context.Background(), routerUUID, expiration)
	}

	_, err := rdbPipe.Exec(context.Background())
	return err
}
