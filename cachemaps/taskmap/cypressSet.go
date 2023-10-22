package taskmap

import (
	"context"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var nameOfSet = "cypress"

func AddToCypressSet(cypressTaskId string) {
	_, err := config.RedisCypressSetClient.SAdd(context.Background(), nameOfSet, cypressTaskId).Result()
	if err != nil {
		log.WithField(config.TaskIdKey, cypressTaskId).WithError(err).Warn("Failed to add cypress task to set")
	}
}

func CypressSetKeys() ([]string, error) {
	return config.RedisCypressSetClient.SMembers(context.Background(), nameOfSet).Result()
}

func RemoveFromCypressSet(cypressTaskId string) {
	_, err := config.RedisCypressSetClient.SRem(context.Background(), nameOfSet, cypressTaskId).Result()
	if err != nil {
		log.WithField(config.TaskIdKey, cypressTaskId).WithError(err).Warn("Failed to remove cypress task from set")
	}
}
