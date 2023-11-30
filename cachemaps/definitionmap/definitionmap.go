package definitionmap

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var (
	definitionsMap             map[string]int64
	TaskDefenititonRefreshDone = "done"
	mutex                      = &sync.RWMutex{}
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

// Add revision to redis
func AddRevision(overrideDefenititonHash string, revision int64) error {
	hr := hashRevision{
		Hash:     overrideDefenititonHash,
		Revision: revision,
	}

	data, err := json.Marshal(&hr)
	if err != nil {
		return err
	}

	err = config.RedisDefinitionClient.Set(context.Background(), overrideDefenititonHash, data, 0).Err()
	if err != nil {
		return err
	}

	return nil
}

// Returns all definitions from redis as map[hash]revision
func getDefinitions() (map[string]int64, error) {
	keys, err := config.RedisDefinitionClient.Keys(context.Background(), "*").Result()
	if err != nil {
		log.WithError(err).Error("Failed to get all keys")
		return nil, err
	}

	rdbPipe := config.RedisDefinitionClient.Pipeline()
	for _, hash := range keys {
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

		if data == TaskDefenititonRefreshDone {
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

// Remove definition from redis by overrideDefenititonHash
func Remove(key string) error {
	return config.RedisDefinitionClient.Del(context.Background(), key).Err()
}

// Adds to redis taskDefenititonRefreshDone record,
// which means that refresh was successfully performed and all supported task definition revisions are placed in redis
func SetRefreshDone() error {
	err := config.RedisDefinitionClient.Set(context.Background(), TaskDefenititonRefreshDone, TaskDefenititonRefreshDone, 0).Err()
	if err != nil {
		return err
	}
	return nil
}

// Checks for existing of taskDefenititonRefreshDone record.
// If found:
// 1) Initializes definitionsMap, that will be used for revision search on task create calls. One for every router
// 2) Launches new thread, in which every 10 minutes definitionsMap is updated by definitions from redis
func IsRefreshDone() bool {
	exists, err := config.RedisDefinitionClient.Exists(context.Background(), TaskDefenititonRefreshDone).Result()
	if err != nil {
		return false
	}

	if exists == 0 {
		return false
	}

	defMap, err := getDefinitions()
	if err != nil {
		log.WithError(err).Warn("Failed to get all definitions")
		return false
	}

	definitionsMap = defMap
	go func() {
		for {
			time.Sleep(10 * time.Minute)
			defMap, err := getDefinitions()
			if err != nil {
				log.Warn("Failed to update definitions info")
				continue
			}
			definitionsMap = defMap
		}
	}()

	return true
}
