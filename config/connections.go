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

func InitRedisMapperClient() error {
	client, err := newRedisClient(RedisMapperDB)
	if err != nil {
		return err
	}
	RedisMapperClient = client
	return nil
}

func InitRedisDefinitionClient() error {
	client, err := newRedisClient(RedisDefinitionClientDB)
	if err != nil {
		return err
	}
	RedisDefinitionClient = client
	return nil
}

func InitRedisUtilityClient() error {
	client, err := newRedisClient(RedisResourcesClientDB)
	if err != nil {
		return err
	}
	RedisResourcesClient = client
	return nil
}

func InitRedisResourcesClient() error {
	client, err := newRedisClient(RedisUtilityClientDB)
	if err != nil {
		return err
	}
	RedisUtilityClient = client
	return nil
}

/*
* Close all Redis and Database connections.
 */
func CloseConnections() {
	if RedisMapperClient != nil {
		RedisMapperClient.Close()
	}
	if RedisDefinitionClient != nil {
		RedisDefinitionClient.Close()
	}
	if RedisResourcesClient != nil {
		RedisResourcesClient.Close()
	}
	if RedisUtilityClient != nil {
		RedisUtilityClient.Close()
	}
	if DbConnection != nil {
		DbConnection.Close()
	}
}

func newRedisClient(db int) (*redis.Client, error) {
	//default PoolTimeout - 4 seconds
	client := redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          db,
		PoolTimeout: 10 * time.Second,
	})

	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to ping redis %d connection", db)
	}
	return client, err
}

func InitCache() error {

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
