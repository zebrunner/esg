package config

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
	redis "github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

var (
	RedisMapperClient     *redis.Client
	RedisDefinitionClient *redis.Client
	RedisResourcesClient  *redis.Client
	RedisUtilityClient    *redis.Client
	DbConnection          *sqlx.DB
)

func InitCache() error {
	//default PoolTimeout - 4 seconds

	// DB 0 - for mapper
	RedisMapperClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          0,
		PoolTimeout: 10 * time.Second,
	})

	_, err := RedisMapperClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis RedisMapperClient connection")
		return err
	}

	// DB 1 - for definitions
	RedisDefinitionClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          1,
		PoolTimeout: 10 * time.Second,
	})

	_, err = RedisDefinitionClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis definitions connection")
		return err
	}

	// DB 2 - for tasks that are in register queue
	// Such tasks cannot get into the provisioning pool, but still need to be calculated by scaler
	RedisResourcesClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          2,
		PoolTimeout: 10 * time.Second,
	})

	_, err = RedisResourcesClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis resources connection")
		return err
	}

	// DB 3 - for utility records (key lockers, key markers, etc...)
	RedisUtilityClient = redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          3,
		PoolTimeout: 10 * time.Second,
	})

	_, err = RedisUtilityClient.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Error("Failed to ping redis utility connection")
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
