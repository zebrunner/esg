package definitionmap

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/cachemaps"
	"github.com/zebrunner/esg/config"
)

var (
	definitionsMap map[string]int64
	mutex          = &sync.RWMutex{}
)

type hashRevision struct {
	Hash     string
	Revision int64
}

// Find revision in definitionsMap (without redis usage).
func FindRevision(hash string) (int64, bool) {
	if definitionsMap == nil {
		return -1, false
	}

	mutex.RLock()
	revision, ok := definitionsMap[hash]
	mutex.RUnlock()

	return revision, ok
}

// Add's new revisions to redis/update's ttl for existing ones
func WriteAll(definitionsMap map[string]int64, expiration time.Duration) error {
	rdbPipe := config.RedisDefinitionClient.Pipeline()
	hashRevisionMap := make(map[string]hashRevision, len(definitionsMap))
	for k, v := range definitionsMap {
		hashRevisionMap[k] = hashRevision{Hash: k, Revision: v}
	}

	return cachemaps.WriteAll(rdbPipe, hashRevisionMap, expiration)
}

// Returns all definitions from redis as map[hash]revision
func getDefinitions() (map[string]int64, error) {
	keysSet := make(map[string]string)
	iter := config.RedisDefinitionClient.Scan(context.Background(), 0, "*", 50).Iterator()
	for iter.Next(context.Background()) {
		key := iter.Val()
		keysSet[key] = key
	}

	if err := iter.Err(); err != nil {
		log.WithError(err).Error("Failed to get all keys")
		return nil, err
	}

	rdbPipe := config.RedisDefinitionClient.Pipeline()
	for hash := range keysSet {
		rdbPipe.Get(context.Background(), hash)
	}

	cmds, err := rdbPipe.Exec(context.Background())
	if err != nil {
		log.WithError(err).Warn("Failed to execute pipe")
		return nil, err
	}

	definitionsMap := make(map[string]int64)
	for _, cmd := range cmds {
		data, err := cmd.(*redis.StringCmd).Result()
		if err != nil {
			log.WithError(err).Warn("Failed to get cached task")
			continue
		}

		var hr hashRevision
		err = json.Unmarshal([]byte(data), &hr)
		if err != nil {
			return nil, err
		}
		definitionsMap[hr.Hash] = hr.Revision
	}

	return definitionsMap, nil
}

// every `interval` in minutes we update local definitionsMap syncing it with redis cache to minimize redis calls at run-time
func ActualizeDefinitionsMap(interval time.Duration) {
	for {
		defMap, err := getDefinitions()
		if err != nil {
			log.Warn("Failed to get all definitions")
			continue
		} else {
			definitionsMap = defMap
		}

		time.Sleep(interval)
	}
}
