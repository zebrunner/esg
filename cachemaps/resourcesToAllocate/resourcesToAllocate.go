package resourcesToAllocate

import (
	"context"
	"encoding/json"

	"github.com/go-redis/redis/v8"
	"github.com/zebrunner/esg/config"
)

type Resources struct {
	UUID   string
	Cpu    int64
	Memory int64
}

func AddEntity(resources *Resources) error {
	data, err := json.Marshal(&resources)
	if err != nil {
		return err
	}

	err = config.ResourcesConnection.Set(context.Background(), resources.UUID, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

func RemoveEntity(uuid string) error {
	return config.ResourcesConnection.Del(context.Background(), uuid).Err()
}

func GetAllEntities() ([]*Resources, error) {
	keys, err := config.ResourcesConnection.Keys(context.Background(), "*").Result()
	if err != nil {
		return nil, err
	}

	resources := make([]*Resources, 0, len(keys))
	for _, uuid := range keys {
		data, err := config.ResourcesConnection.Get(context.Background(), uuid).Result()
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
