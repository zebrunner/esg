package utilsmap

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

const (
	TaskDefenititonRefreshDone = "done"
	ScalerVersion              = "scalerVersion"
)

func AcquireLock(key string, expiration time.Duration) bool {
	ok, err := config.REDIS_UTILITY.GetConnection().SetNX(context.Background(), key, "lock", expiration).Result()
	if err != nil {
		logrus.WithError(err).Error("Failed to obtain lock")
		return false
	}

	return ok
}

func ReleaseLock(key string) error {
	return config.REDIS_UTILITY.GetConnection().Del(context.Background(), key).Err()
}

// Adds to redis taskDefenititonRefreshDone record,
// which means that TaskDefenition refresh was successfully performed and all supported task definition revisions are placed in redis db
func SetTaskDefenitionRefreshDone() error {
	return config.REDIS_UTILITY.GetConnection().Set(context.Background(), TaskDefenititonRefreshDone, TaskDefenititonRefreshDone, 0).Err()
}

func SetScalerVersion() error {
	return config.REDIS_UTILITY.GetConnection().Set(context.Background(), ScalerVersion, config.Version, 0).Err()
}

func GetScalerVersion() (string, error) {
	return config.REDIS_UTILITY.GetConnection().Get(context.Background(), ScalerVersion).Result()
}

func UnsetTaskDefenitionRefreshDone() error {
	return config.REDIS_UTILITY.GetConnection().Del(context.Background(), TaskDefenititonRefreshDone).Err()
}

// Checks for presense of TaskDefenititonRefreshDone key in redis db
func IsTaskDefenitionRefreshDone() bool {
	exists, err := config.REDIS_UTILITY.GetConnection().Exists(context.Background(), TaskDefenititonRefreshDone).Result()
	if err != nil {
		return false
	}

	return exists != 0
}
