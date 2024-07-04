package resourcesToAllocate

import (
	"context"
	"encoding/json"
	"time"

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
	resourceWorker = cachemaps.CreateRedisWorker(writeRecords)
	go resourceWorker.Start(4 * time.Second)
}

func writeRecords(items map[string]ResourceItem) error {
	rdbPipe := config.RedisCluster.Pipeline()
	for key, item := range items {
		if item.resourceToAdd != nil {
			data, err := json.Marshal(item.resourceToAdd)
			if err != nil {
				log.WithError(err).WithField(config.RouterUUID, item.resourceToAdd.RouterUUID).Warn("Failed to marshal resource")
				continue
			}
			rdbPipe.Set(context.Background(), key, data, -1)
			rdbPipe.SAdd(context.Background(), cachemaps.UNALLOCATED_RESOURCES.String(), key)
		} else if item.resourceToDelete != nil {
			rdbPipe.Del(context.Background(), key)
			rdbPipe.SRem(context.Background(), cachemaps.UNALLOCATED_RESOURCES.String(), key)
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
