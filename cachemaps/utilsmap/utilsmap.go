package utilsmap

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

const TaskDefenititonRefreshDone = "done"

func AcquireLock(key string, expiration time.Duration) bool {
	ok, err := config.RedisUtilityClient.SetNX(context.Background(), key, "lock", expiration).Result()
	if err != nil {
		logrus.WithError(err).Error("Failed to obtain lock")
		return false
	}

	return ok
}

func ReleaseLock(key string) error {
	return config.RedisUtilityClient.Del(context.Background(), key).Err()
}

// Adds to redis taskDefenititonRefreshDone record,
// which means that TaskDefenition refresh was successfully performed and all supported task definition revisions are placed in redis db
func SetTaskDefenitionRefreshDone() error {
	return config.RedisUtilityClient.Set(context.Background(), TaskDefenititonRefreshDone, TaskDefenititonRefreshDone, 0).Err()
}

func UnsetTaskDefenitionRefreshDone() error {
	return config.RedisUtilityClient.Del(context.Background(), TaskDefenititonRefreshDone).Err()
}

// Checks for presense of TaskDefenititonRefreshDone key in redis db
func IsTaskDefenitionRefreshDone() bool {
	exists, err := config.RedisUtilityClient.Exists(context.Background(), TaskDefenititonRefreshDone).Result()
	if err != nil {
		return false
	}

	return exists != 0
}
