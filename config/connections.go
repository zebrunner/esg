package config

import (
	"context"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
	redis "github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

type redisDB int

const (
	REDIS_MAPPER_CLIENT redisDB = iota
	REDIS_DEFINITIONS_CLIENT
	// for tasks that are in register queue
	// Such tasks cannot get into the provisioning pool, but still need to be calculated by scaler
	REDIS_RESOURCES_CLIENT
	// for utility records (key lockers, key markers, etc...)
	REDIS_UTILITY_CLIENT
)

var (
	redisMapperClient      *redis.Client
	redisDefinitionsClient *redis.Client
	redisResourcesClient   *redis.Client
	redisUtilityClient     *redis.Client
	DbConnection           *sqlx.DB
)

func (db redisDB) InitConnection() error {

	if db.GetConnection() != nil {
		return fmt.Errorf("'%d' redis connection already initialized", db)
	}
	//default PoolTimeout - 4 seconds
	client := redis.NewClient(&redis.Options{
		Addr:        Conf.RedisConnectionString,
		Password:    "",
		DB:          int(db),
		PoolTimeout: 10 * time.Second,
	})

	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to ping redis %d connection", db)
		client.Close()
	} else {
		db.setConnection(client)
	}
	return err
}

func (db redisDB) setConnection(client *redis.Client) {
	switch db {
	case REDIS_MAPPER_CLIENT:
		redisMapperClient = client
	case REDIS_DEFINITIONS_CLIENT:
		redisDefinitionsClient = client
	case REDIS_RESOURCES_CLIENT:
		redisResourcesClient = client
	case REDIS_UTILITY_CLIENT:
		redisUtilityClient = client
	}
}

func (db redisDB) GetConnection() *redis.Client {
	switch db {
	case REDIS_MAPPER_CLIENT:
		return redisMapperClient
	case REDIS_DEFINITIONS_CLIENT:
		return redisDefinitionsClient
	case REDIS_RESOURCES_CLIENT:
		return redisResourcesClient
	case REDIS_UTILITY_CLIENT:
		return redisUtilityClient
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

/*
* Close all Redis and Database connections.
 */
func CloseConnections() {
	if redisMapperClient != nil {
		redisMapperClient.Close()
	}
	if redisDefinitionsClient != nil {
		redisDefinitionsClient.Close()
	}
	if redisResourcesClient != nil {
		redisResourcesClient.Close()
	}
	if redisUtilityClient != nil {
		redisUtilityClient.Close()
	}
	if DbConnection != nil {
		log.Info("Closing database connection.")
		DbConnection.Close()
	}
}
