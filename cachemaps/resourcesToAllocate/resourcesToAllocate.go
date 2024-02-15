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
	keys, err := config.RedisResourcesClient.Keys(context.Background(), fmt.Sprintf("%s*", capacityProvider)).Result()
	if err != nil {
		return nil, err
	}

	resources := make([]*ResourcesToAllocate, 0, len(keys))
	for _, uuid := range keys {
		data, err := config.RedisResourcesClient.Get(context.Background(), uuid).Result()
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
