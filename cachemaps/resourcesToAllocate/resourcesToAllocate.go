package resourcesToAllocate

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/config"
)

type ResourcesToAllocate struct {
	RouterUUID       string
	Cpu              int64
	Memory           int64
	CapacityProvider string
}

func (rsa *ResourcesToAllocate) generateRedisId() string {
	return fmt.Sprintf("%s_%s", rsa.CapacityProvider, rsa.RouterUUID)
}

func GetEntitiesOfCapacityProvider(capacityProvider string) ([]*ResourcesToAllocate, error) {
	keysSet := make(map[string]string)
	iter := config.REDIS_RESOURCES.GetConnection().Scan(context.Background(), 0, fmt.Sprintf("%s*", capacityProvider), 50).Iterator()
	for iter.Next(context.Background()) {
		key := iter.Val()
		keysSet[key] = key
	}

	if err := iter.Err(); err != nil {
		return nil, err
	}

	resources := make([]*ResourcesToAllocate, 0, len(keysSet))
	for uuid := range keysSet {
		data, err := config.REDIS_RESOURCES.GetConnection().Get(context.Background(), uuid).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return nil, err
		}

		var resource ResourcesToAllocate
		err = json.Unmarshal([]byte(data), &resource)
		if err != nil {
			return nil, err
		}

		resources = append(resources, &resource)
	}

	return resources, nil
}
