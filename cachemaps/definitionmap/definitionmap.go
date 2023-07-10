package defenitionmap

import (
	"context"
	"encoding/json"

	"github.com/zebrunner/esg/config"
)

var taskDefenititonRefreshDone = "done"

type defenitionRevision struct {
	OverrideDefinitionHash string
	Revision               int64
}

func FindRevision(hash string) (int64, error) {
	sessionData, err := config.RedisTasksConnection.Get(context.Background(), hash).Result()
	if err != nil {
		return -1, err
	}

	var dr defenitionRevision
	err = json.Unmarshal([]byte(sessionData), &dr)
	if err != nil {
		return -1, err
	}

	return dr.Revision, nil
}

func AddDefinition(overrideDefenititonHash string, revision int64) error {
	dr := &defenitionRevision{
		OverrideDefinitionHash: overrideDefenititonHash,
		Revision:               revision,
	}

	data, err := json.Marshal(dr)
	if err != nil {
		return err
	}

	err = config.RedisTasksConnection.Set(context.Background(), dr.OverrideDefinitionHash, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func SetRefreshDone() error {
	err := config.RedisTasksConnection.Set(context.Background(), taskDefenititonRefreshDone, true, 0).Err()
	if err != nil {
		return err
	}
	return nil
}

func IsRefreshDone() bool {
	_, err := config.RedisTasksConnection.Get(context.Background(), taskDefenititonRefreshDone).Result()

	return err == nil
}
