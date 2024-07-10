package utilsmap

import (
	"context"

	"github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

const (
	ScalerVersion          serviceVersionKey = "scalerVersion"
	TaskDefinitionsVersion serviceVersionKey = "taskDefinitionsVersion"
)

type serviceVersionKey string

func (svk serviceVersionKey) String() string {
	return string(svk)
}

func (svk serviceVersionKey) Set(version string) error {
	return config.RedisCluster.Set(context.Background(), svk.String(), config.Version, 0).Err()
}

func (svk serviceVersionKey) Get() (string, error) {
	return config.RedisCluster.Get(context.Background(), svk.String()).Result()
}

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
