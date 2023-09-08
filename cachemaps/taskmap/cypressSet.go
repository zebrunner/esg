package taskmap

import (
	"context"

	log "github.com/sirupsen/logrus"
	"github.com/zebrunner/esg/config"
)

var nameOfSet = "cypress"

func AddToSet(cypressTaskId string) {
	_, err := config.CypressSetConnection.SAdd(context.Background(), nameOfSet, cypressTaskId).Result()
	if err != nil {
		log.WithField(config.TaskIdKey, cypressTaskId).WithError(err).Warn("Failed to add cypress task to set")
	}
}

func CypressSetKeys() ([]string, error) {
	return config.CypressSetConnection.SMembers(context.Background(), nameOfSet).Result()
}

func RemoveFromSet(cypressTaskId string) {
	_, err := config.CypressSetConnection.SRem(context.Background(), nameOfSet, cypressTaskId).Result()
	if err != nil {
		log.WithField(config.TaskIdKey, cypressTaskId).WithError(err).Warn("Failed to remove cypress task from set")
	}
}
