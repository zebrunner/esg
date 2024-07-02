package definitionmap

import (
	"sync"
	"time"

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

// Add new revisions
func WriteAll(definitions map[string]int64) error {
	hashRevisionMap := make(map[string]hashRevision, len(definitions))
	for k, v := range definitions {
		hashRevisionMap[k] = hashRevision{Hash: k, Revision: v}
	}

	return cachemaps.WriteAll(config.RedisCluster.Pipeline(), cachemaps.DEFINITION, hashRevisionMap)
}

// Expire all revisions
func ExpireAll(ttl time.Duration) error {
	definitions, err := getDefinitions()
	if err != nil {
		return err
	}

	i := 0
	definitionKeys := make([]string, len(definitions))
	for key := range definitions {
		definitionKeys[i] = key
		i++
	}

	return cachemaps.ExpireAll(config.RedisCluster.Pipeline(), cachemaps.DEFINITION, definitionKeys, ttl)
}

// Returns all definitions from redis as map[hash]revision
func getDefinitions() (map[string]int64, error) {
	keys, err := cachemaps.GetKeys(cachemaps.DEFINITION)
	if err != nil {
		log.WithError(err).Error("Failed to get all definition keys")
		return nil, err
	}

	hashRevisionArr, err := cachemaps.FindAll[hashRevision](config.RedisCluster.Pipeline(), keys)
	if err != nil {
		log.WithError(err).Error("Failed to get all definitions")
		return nil, err
	}

	hashRevisionMap := make(map[string]int64, len(hashRevisionArr))
	for _, hashRevision := range hashRevisionArr {
		hashRevisionMap[hashRevision.Hash] = hashRevision.Revision
	}

	return hashRevisionMap, nil
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
