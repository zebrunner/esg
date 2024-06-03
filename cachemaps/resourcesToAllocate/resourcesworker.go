package resourcesToAllocate

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

var (
	resourceWorker cachemaps.RedisWorker[ResourceItem]
)

type ResourceItem struct {
	resourceToAdd    *ResourcesToAllocate
	resourceToDelete *string
}

// Inits worker and starts it in new thread (resource worker).
// Resource Worker -> Reserves new instances if aws provisioning task pool is exceeded. RemoveEntity should be always performed after AddEntity.
func InitResourceWorker() {
	resourceWorker = cachemaps.CreateRedisWorker[ResourceItem](config.REDIS_RESOURCES_CLIENT.GetConnection(), writeRecords)
	go resourceWorker.Start(4 * time.Second)
}

func writeRecords(rdsConn *redis.Conn, items map[string]ResourceItem) error {
	rdbPipe := rdsConn.Pipeline()
	for key, item := range items {
		if item.resourceToAdd != nil {
			data, err := json.Marshal(item.resourceToAdd)
			if err != nil {
				log.WithError(err).WithField(config.RouterUUID, item.resourceToAdd.RouterUUID).Warn("Failed to marshal resource")
				continue
			}
			rdbPipe.Set(context.Background(), key, data, 10*time.Minute)
		} else if item.resourceToDelete != nil {
			rdbPipe.Expire(context.Background(), key, 10*time.Second)
		}
	}

	_, err := rdbPipe.Exec(context.Background())
	return err
}

func AddEntity(resources *ResourcesToAllocate) error {
	return resourceWorker.AppendToWorker(resources.generateRedisId(), ResourceItem{resourceToAdd: resources})
}

func RemoveEntity(resources *ResourcesToAllocate) error {
	redisId := resources.generateRedisId()
	return resourceWorker.AppendToWorker(redisId, ResourceItem{resourceToDelete: &redisId})
}
