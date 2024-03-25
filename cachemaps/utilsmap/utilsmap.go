package utilsmap

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

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
