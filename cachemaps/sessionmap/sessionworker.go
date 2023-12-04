package sessionmap

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
	writeWorker cachemaps.RedisWorker[SessionItem]
)

type SessionItem struct {
	CachedSession Session
	Expiration    time.Duration
}

// Inits worker and starts execution in new thread (write worker).
// Write worker -> creates new records/rewrites existing ones.
func InitSessionmapWorker() {
	writeWorker = cachemaps.CreateRedisWorker[SessionItem](config.RedisSessionsClient, writeRecords)
	go writeWorker.Start(time.Second * 1)
}

func writeRecords(rdsConn *redis.Conn, items map[string]SessionItem) error {
	rdbWritePipeline := rdsConn.Pipeline()
	for _, item := range items {
		data, err := json.Marshal(&item.CachedSession)
		if err != nil {
			log.WithError(err).WithField(config.SessionIdKey, item.CachedSession.SessionID).Error("Failed to marshal record")
			continue
		}

		rdbWritePipeline.Set(context.Background(), item.CachedSession.SessionID, data, item.Expiration)
	}

	_, err := rdbWritePipeline.Exec(context.Background())
	return err
}

func WriteSession(session Session, expiration time.Duration) error {
	return writeWorker.AppendToWorker(session.SessionID, SessionItem{CachedSession: session, Expiration: expiration})
}
