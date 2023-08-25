package config

import (
	"context"
	redis "github.com/go-redis/redis/v8"
	"github.com/jmoiron/sqlx"
	log "github.com/sirupsen/logrus"
)

var (
	RedisSessionsConnection    *redis.Client
	RedisTasksConnection       *redis.Client
	RedisTasksMapperConnection *redis.Client
	RedisDefinitionConnection  *redis.Client
	DbConnection               *sqlx.DB
)

func InitCache() error {
	// DB 0 - for sessions
	RedisSessionsConnection = redis.NewClient(&redis.Options{
		Addr:     Conf.RedisConnectionString,
		Password: "",
		DB:       0,
	})

	_, err := RedisSessionsConnection.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis sessions connection")
		return err
	}

	// DB 1 - for tasks
	RedisTasksConnection = redis.NewClient(&redis.Options{
		Addr:     Conf.RedisConnectionString,
		Password: "",
		DB:       1,
	})

	_, err = RedisTasksConnection.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis tasks connection")
		return err
	}

	// DB 2 - for definitions
	RedisDefinitionConnection = redis.NewClient(&redis.Options{
		Addr:     Conf.RedisConnectionString,
		Password: "",
		DB:       2,
	})

	_, err = RedisDefinitionConnection.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis definitions connection")
		return err
	}

	// DB 3 - for task's mapper
	RedisTasksMapperConnection = redis.NewClient(&redis.Options{
		Addr:     Conf.RedisConnectionString,
		Password: "",
		DB:       3,
	})

	_, err = RedisTasksMapperConnection.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis tasksmepper connection")
		return err
	}

	return nil
}

func InitDBConnection(connectionString string) error {
	var err error
	DbConnection, err = sqlx.Open("pgx", connectionString)
	if err != nil {
		return err
	}

	err = DbConnection.Ping()
	if err != nil {
		DbConnection.Close()
		return err
	}
	return nil
}
