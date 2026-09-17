package utilsmap

import (
	"context"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
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
	return config.RedisCluster.Set(context.Background(), svk.String(), version, 0).Err()
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

// AcquireExpiringLock obtains a distributed lock that Redis releases if its owner disappears.
func AcquireExpiringLock(ctx context.Context, key string, owner string, expiration time.Duration) (bool, error) {
	return config.RedisCluster.SetNX(ctx, key, owner, expiration).Result()
}

// ReleaseExpiringLock releases a distributed lock only when owner still owns it.
func ReleaseExpiringLock(ctx context.Context, key string, owner string) error {
	const releaseScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
  return redis.call("del", KEYS[1])
end
return 0
`

	err := config.RedisCluster.Eval(ctx, releaseScript, []string{key}, owner).Err()
	if errors.Is(err, redis.Nil) {
		return nil
	}

	return err
}
