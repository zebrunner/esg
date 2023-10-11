package config

import (
	"context"
	// "time"

	"github.com/jmoiron/sqlx"
	redis "github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

var (
	RedisSessionsClient   *redis.Client
	RedisTasksClient      *redis.Client
	RedisCypressSetClient *redis.Client
	RedisIdMapperClient   *redis.Client
	RedisDefinitionClient *redis.Client
	RedisResourcesClient  *redis.Client
	DbConnection          *sqlx.DB
)

func InitCache() error {
	// DB 0 - for sessions
	RedisSessionsClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          0,
//		PoolTimeout: 15 * time.Second,
	})

	_, err := RedisSessionsClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis sessions connection")
		return err
	}

	// DB 1 - for tasks
	RedisTasksClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          1,
//		PoolTimeout: 15 * time.Second,
	})

	_, err = RedisTasksClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis tasks connection")
		return err
	}

	// DB 2 - for definitions
	RedisDefinitionClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          2,
//		PoolTimeout: 15 * time.Second,
	})

	_, err = RedisDefinitionClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis definitions connection")
		return err
	}

	// DB 3 - for task's mapper
	RedisIdMapperClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          3,
//		PoolTimeout: 15 * time.Second,
	})

	_, err = RedisIdMapperClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis tasksmapper connection")
		return err
	}

	// DB 4 - for cypress set
	RedisCypressSetClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          4,
//		PoolTimeout: 15 * time.Second,
	})

	_, err = RedisCypressSetClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis cypressSet connection")
		return err
	}

	// DB 5 - for tasks that are in register queue
	// Such tasks cannot get into the provisioning pool, but still need to be calculated by scaler
	RedisResourcesClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          5,
//		PoolTimeout: 15 * time.Second,
	})

	_, err = RedisResourcesClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis resources connection")
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
