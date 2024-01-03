package mapper

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
	writeWorker  cachemaps.RedisWorker[MapperItem]
	expireWorker cachemaps.RedisWorker[MapperItem]
)

type MapperItem struct {
	Mapper     IdMapper
	Expiration time.Duration
}

// Inits 2 workers and starts them in new thread (write and expire worker).
// Write worker -> creates new records/rewrites existing ones.
// Expire worker -> sets expiration time for existing records. Expiration value of <= 0 deletes the record immediately
func InitUUIDMapWorkers() {
	writeWorker = cachemaps.CreateRedisWorker[MapperItem](config.RedisIdMapperClient, writeRecords)
	go writeWorker.Start(1 * time.Second)

	expireWorker = cachemaps.CreateRedisWorker[MapperItem](config.RedisIdMapperClient, expireRecords)
	go expireWorker.Start(1 * time.Second)
}

func writeRecords(rdsConn *redis.Conn, items map[string]MapperItem) error {
	rdbWritePipeline := rdsConn.Pipeline()
	for routerUUID, item := range items {
		data, err := json.Marshal(&item.Mapper)
		if err != nil {
			log.WithError(err).WithField(config.RouterUUID, routerUUID).Error("Failed to marshal record")
			continue
		}

		rdbWritePipeline.Set(context.Background(), routerUUID, data, item.Expiration)
	}

	_, err := rdbWritePipeline.Exec(context.Background())
	return err
}

func expireRecords(rdsConn *redis.Conn, items map[string]MapperItem) error {
	rdbExpirePipe := rdsConn.Pipeline()
	for routerUUID, item := range items {
		rdbExpirePipe.Expire(context.Background(), routerUUID, item.Expiration)
	}

	_, err := rdbExpirePipe.Exec(context.Background())
	return err
}

func WriteMapperRecord(mapper IdMapper, expiration time.Duration) error {
	return writeWorker.AppendToWorker(mapper.RouterUUID, MapperItem{Mapper: mapper, Expiration: expiration})
}

func ExpireMapperRecord(routerUUID string, expiration time.Duration) error {
	return expireWorker.AppendToWorker(routerUUID, MapperItem{Expiration: expiration})
}
