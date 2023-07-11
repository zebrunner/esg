package definitionmap

import (
	"context"
	"github.com/zebrunner/esg/config"
)

var taskDefenititonRefreshDone = "done"

func FindRevision(hash string) (int64, error) {
	revision, err := config.RedisDefinitionConnection.Get(context.Background(), hash).Int64()
	if err != nil {
		return -1, err
	}

	return revision, nil
}

func AddDefinition(overrideDefenititonHash string, revision int64) error {
	err := config.RedisDefinitionConnection.Set(context.Background(), overrideDefenititonHash, revision, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func SetRefreshDone() error {
	err := config.RedisDefinitionConnection.Set(context.Background(), taskDefenititonRefreshDone, taskDefenititonRefreshDone, 0).Err()
	if err != nil {
		return err
	}
	return nil
}

func IsRefreshDone() bool {
	exists, err := config.RedisDefinitionConnection.Exists(context.Background(), taskDefenititonRefreshDone).Result()
	if err != nil {
		return false
	}

	return exists != 0
}
