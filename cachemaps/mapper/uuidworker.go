package mapper

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

var (
	writeMutex   = sync.Mutex{}
	expireMutex  = sync.Mutex{}
	writeWorker  worker
	expireWorker worker
)

type WriteItme struct {
	Mapper     IdMapper
	Expiration time.Duration
}

type worker struct {
	conn         *redis.Conn
	itemsToWrite map[string]WriteItme
	// signals that work is done. Recieved item should not be used
	responseCh chan interface{}
	errCh      chan error
}

func InitUuidmapWorkers() {
	writeWorker = worker{
		conn:         config.RedisIdMapperClient.Conn(),
		itemsToWrite: make(map[string]WriteItme, 0),
		responseCh:   make(chan interface{}),
		errCh:        make(chan error),
	}
	go startWriteWorker()

	expireWorker = worker{
		conn:         config.RedisIdMapperClient.Conn(),
		itemsToWrite: make(map[string]WriteItme, 0),
		responseCh:   make(chan interface{}),
		errCh:        make(chan error),
	}

	go startExpireWorker()
}

func (w *worker) flush() {
	w.itemsToWrite = make(map[string]WriteItme, 0)
	w.responseCh = make(chan interface{})
	w.errCh = make(chan error)
}

func startWriteWorker() {
	for {
		time.Sleep(1 * time.Second)
		writeMutex.Lock()
		items := writeWorker.itemsToWrite
		responseCh := writeWorker.responseCh
		errCh := writeWorker.errCh
		writeWorker.flush()
		writeMutex.Unlock()

		if len(items) == 0 {
			continue
		}

		rdbWritePipeline := writeWorker.conn.Pipeline()
		for _, item := range items {
			data, _ := json.Marshal(&item.Mapper)
			rdbWritePipeline.Set(context.Background(), item.Mapper.RouterUUID, data, item.Expiration)
		}

		_, err := rdbWritePipeline.Exec(context.Background())
		if err != nil {
			cachemaps.SendMessageToAllChanns(errCh, err)
		} else {
			cachemaps.SendMessageToAllChanns(responseCh, 0)
		}
	}
}

func startExpireWorker() {
	for {
		time.Sleep(1 * time.Second)
		expireMutex.Lock()
		items := expireWorker.itemsToWrite
		responseCh := expireWorker.responseCh
		errCh := expireWorker.errCh
		expireWorker.flush()
		expireMutex.Unlock()

		if len(items) == 0 {
			continue
		}

		rdbExpirePipe := expireWorker.conn.Pipeline()
		for routerUuid, item := range items {
			rdbExpirePipe.Expire(context.Background(), routerUuid, item.Expiration)
		}

		_  , err := rdbExpirePipe.Exec(context.Background())
		if err != nil {
			cachemaps.SendMessageToAllChanns(errCh, err)
		} else {
			cachemaps.SendMessageToAllChanns(responseCh, 0)
		}
	}
}

func WriteMapper(routerUuid string, item WriteItme) (<-chan interface{}, <-chan error) {
	writeMutex.Lock()
	writeWorker.itemsToWrite[routerUuid] = item
	responseCh := writeWorker.responseCh
	errCh := writeWorker.errCh
	writeMutex.Unlock()
	return responseCh, errCh
}

func ExpireMapper(routerUuid string, item WriteItme) (<-chan interface{}, <-chan error) {
	expireMutex.Lock()
	expireWorker.itemsToWrite[routerUuid] = item
	responseCh := expireWorker.responseCh
	errCh := expireWorker.errCh
	expireMutex.Unlock()
	return responseCh, errCh
}
