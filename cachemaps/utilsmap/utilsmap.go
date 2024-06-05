package utilsmap

import (
	"context"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

const (
	ScalerVersion              = "scalerVersion"
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


func SetScalerVersion() error {
	return config.RedisUtilityClient.Set(context.Background(), ScalerVersion, config.Version, 0).Err()
}

func GetScalerVersion() (string, error) {
	return config.RedisUtilityClient.Get(context.Background(), ScalerVersion).Result()
}
