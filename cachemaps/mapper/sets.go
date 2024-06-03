package mapper

import (
	"context"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

type SetType string

const (
	SESSION SetType = "session"
	TASK    SetType = "task"
)

func AppendToSet(st SetType, uuid string) error {
	return config.REDIS_MAPPER.GetConnection().SAdd(context.Background(), string(st), uuid).Err()
}

func RemoveFromSet(st SetType, uuid string) error {
	return config.REDIS_MAPPER.GetConnection().SRem(context.Background(), string(st), uuid).Err()
}

func GetKeys(st SetType) ([]string, error) {
	keysSet := make(map[string]string)

	iter := config.REDIS_MAPPER.GetConnection().SScan(context.Background(), string(st), 0, "*", 50).Iterator()
	for iter.Next(context.Background()) {
		key := iter.Val()
		keysSet[key] = key
	}

	if err := iter.Err(); err != nil {
		log.WithField("setType", st).WithError(err).Error("Failed to get keys")
		return nil, err
	}

	i := 0
	keys := make([]string, len(keysSet))
	for key := range keysSet {
		keys[i] = key
		i++
	}

	return keys, nil
}
