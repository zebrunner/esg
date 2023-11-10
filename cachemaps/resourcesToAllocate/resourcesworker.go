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
	resourceWorker = cachemaps.CreateRedisWorker[ResourceItem](config.RedisResourcesClient, writeRecords)
	// iteration pause is equal to scale up pause
	go resourceWorker.Start(10 * time.Second)
}

func writeRecords(rdsConn *redis.Conn, items map[string]ResourceItem) error {
	rdbPipe := rdsConn.Pipeline()
	for _, item := range items {
		if item.resourceToAdd != nil {
			data, err := json.Marshal(item.resourceToAdd)
			if err != nil {
				log.WithError(err).WithField(config.RouterUUID, item.resourceToAdd.RouterUUID).Warn("Failed to marshal resource")
				continue
			}
			rdbPipe.Set(context.Background(), item.resourceToAdd.RouterUUID, data, 10*time.Minute)
		} else if item.resourceToDelete != nil {
			rdbPipe.Del(context.Background(), *item.resourceToDelete)
		}
	}

	_, err := rdbPipe.Exec(context.Background())
	return err
}

/*
To wait for response implement select switch construction
	select {
	case err := <-errCh:
		...
	case <-responseCh:
	}
*/
func AddEntity(resources *ResourcesToAllocate) (<-chan interface{}, <-chan error) {
	return resourceWorker.AppendToWorker(resources.RouterUUID, ResourceItem{resourceToAdd: resources})
}

/*
To wait for response implement select switch construction
	select {
	case err := <-errCh:
		...
	case <-responseCh:
	}
*/
func RemoveEntity(routerUUID string) (<-chan interface{}, <-chan error) {
	return resourceWorker.AppendToWorker(routerUUID, ResourceItem{resourceToDelete: &routerUUID})
}
