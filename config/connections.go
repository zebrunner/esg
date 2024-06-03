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
	REDIS_DEFINITION_CLIENT
	// for tasks that are in register queue
	// Such tasks cannot get into the provisioning pool, but still need to be calculated by scaler
	REDIS_RESOURCES_CLIENT
	// for utility records (key lockers, key markers, etc...)
	REDIS_UTILITY_CLIENT
)

var (
	connections  = make(map[redisDB]*redis.Client)
	DbConnection *sqlx.DB
)

func (db redisDB) InitConnection() error {
	if connections[db] != nil {
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
		//todo should we close connection if Ping was not successful
		client.Close()
	} else {
		connections[db] = client
	}
	return err
}

func (db redisDB) GetConnection() *redis.Client {
	return connections[db]
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
	for k, v := range connections {
		log.Infof("Closing '%d' redis connection.", k)
		v.Close()

	}
	if DbConnection != nil {
		log.Info("Closing database connection.")
		DbConnection.Close()
	}
}
