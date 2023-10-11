package resourcesToAllocate

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

type ResourceWatchWorker struct {
	resourcesToAdd    map[string]Resources
	resourcesToDelete map[string]string
}

type Resources struct {
	UUID   string
	Cpu    int64
	Memory int64
}

var (
	resourceWorker ResourceWatchWorker
	mutex          = &sync.RWMutex{}
)

func InitResourceWatchWorker() {
	resourceWorker = ResourceWatchWorker{
		resourcesToAdd:    make(map[string]Resources, 0),
		resourcesToDelete: make(map[string]string, 0),
	}
	go sendToRedis()
}

func sendToRedis() {
	for {
		time.Sleep(15 * time.Second)
		// save arr to new pointer and flush from resourceWatchWorker
		mutex.Lock()
		toAdd := resourceWorker.resourcesToAdd
		toDelete := resourceWorker.resourcesToDelete
		resourceWorker.resourcesToAdd = make(map[string]Resources, 0)
		resourceWorker.resourcesToDelete = make(map[string]string, 0)
		mutex.Unlock()

		for uuid := range toDelete {
			if _, ok := toAdd[uuid]; ok {
				delete(toAdd, uuid)
				delete(toDelete, uuid)
			}
		}

		if len(toAdd) == 0 && len(toDelete) == 0 {
			continue
		}

		rdbPipe := config.RedisResourcesClient.Pipeline()
		for _, resource := range toAdd {
			data, err := json.Marshal(resource)
			if err != nil {
				log.WithError(err).WithField(config.RouterUuid, resource.UUID).Warn("Failed to marshal resource")
				continue
			}
			rdbPipe.Set(context.Background(), resource.UUID, data, 10*time.Minute)
		}

		for uuid := range toDelete {
			rdbPipe.Del(context.Background(), uuid)
		}

		_, err := rdbPipe.Exec(context.Background())
		if err != nil {
			log.Warn("Failed to updated resources cache")
		}
	}
}

func AddEntity(resources *Resources) {
	mutex.Lock()
	resourceWorker.resourcesToAdd[resources.UUID] = *resources
	mutex.Unlock()
}

func RemoveEntity(uuid string) {
	mutex.Lock()
	resourceWorker.resourcesToDelete[uuid] = uuid
	mutex.Unlock()
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
