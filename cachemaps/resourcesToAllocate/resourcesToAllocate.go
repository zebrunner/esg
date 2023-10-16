package resourcesToAllocate

import (
	"context"
	"encoding/json"

	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/config"
)

type Resources struct {
	RouterUUID string
	Cpu        int64
	Memory     int64
}

func GetAllEntities() ([]*Resources, error) {
	keys, err := config.RedisResourcesClient.Keys(context.Background(), "*").Result()
	if err != nil {
		return nil, err
	}

	resources := make([]*Resources, 0, len(keys))
	for _, uuid := range keys {
		data, err := config.RedisResourcesClient.Get(context.Background(), uuid).Result()
		if err != nil {
			if err == redis.Nil {
				continue
			}
			return nil, err
		}

		var resource Resources
		err = json.Unmarshal([]byte(data), &resource)
		if err != nil {
			return nil, err
		}

		resources = append(resources, &resource)
	}

	return resources, nil
}
