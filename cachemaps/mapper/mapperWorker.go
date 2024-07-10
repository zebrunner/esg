package mapper

import (
	"context"
	"encoding/json"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

var (
	writeWorker cachemaps.RedisWorker[mapperItem]
)

type mapperItem struct {
	Enity         Mapper
	SetsToAttach  []cachemaps.SetType
	SetsToDettach []cachemaps.SetType
	Expiration    time.Duration
}

func InitMapperWorkers() {
	writeWorker = cachemaps.CreateRedisWorker(writeRecords)

	go writeWorker.Start(1500 * time.Millisecond)
}

func writeRecords(items map[string]mapperItem) error {
	rdbPipeline := config.RedisCluster.Pipeline()
	for routerUUID, item := range items {
		data, err := json.Marshal(&item.Enity)
		if err != nil {
			log.WithError(err).WithField(config.RouterUUID, routerUUID).Error("Failed to marshal record")
			continue
		}

		rdbPipeline.Set(context.Background(), routerUUID, data, item.Expiration)

		for _, set := range item.SetsToAttach {
			rdbPipeline.SAdd(context.Background(), string(set), routerUUID)
		}

		for _, set := range item.SetsToDettach {
			rdbPipeline.SRem(context.Background(), string(set), routerUUID)
		}
	}

	_, err := rdbPipeline.Exec(context.Background())
	return err
}

func WritedByWorker(mapper *Mapper, setsToAttach []cachemaps.SetType, SetsToDettach []cachemaps.SetType, expiration time.Duration) error {
	return writeWorker.AppendToWorker(mapper.RouterUUID,
		mapperItem{
			Enity:         *mapper,
			SetsToAttach:  setsToAttach,
			SetsToDettach: SetsToDettach,
			Expiration:    expiration,
		})
}
