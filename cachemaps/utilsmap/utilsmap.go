package utilsmap

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

const (
	ScalerVersion = "scalerVersion"
)

func AcquireLock(key string) bool {
	res, err := cachemaps.AppendToSet(cachemaps.UTILS, key)
	if err != nil {
		logrus.WithError(err).Error("Failed to obtain lock")
		return false
	}

	// 0 -> key already exists -> key is already busy
	return res != 0
}

func ReleaseLock(key string) error {
	return cachemaps.RemoveFromSet(cachemaps.UTILS, key)
}

func SetScalerVersion() error {
	return config.RedisCluster.Set(context.Background(), ScalerVersion, config.Version, 0).Err()
}

func GetScalerVersion() (string, error) {
	return config.RedisCluster.Get(context.Background(), ScalerVersion).Result()
}
