package config

import (
	"context"
	"crypto/tls"
	"time"

	"github.com/jmoiron/sqlx"
	redis "github.com/redis/go-redis/v9"
	log "github.com/sirupsen/logrus"
)

var (
	RedisCluster *redis.ClusterClient
	DbConnection *sqlx.DB
)

func InitRedisClusterConnection() error {
	RedisCluster = redis.NewClusterClient(&redis.ClusterOptions{
		Addrs:       []string{Conf.RedisConnectionString},
		PoolTimeout: 10 * time.Second,
		TLSConfig:   &tls.Config{MinVersion: tls.VersionTLS12},
	})

	res, err := RedisCluster.Ping(context.Background()).Result()
	if err != nil {
		log.WithField("response", res).WithError(err).Errorf("Failed to ping redis cluster connection")
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

/*
* Close all Redis and Database connections.
 */
func CloseConnections() {
	if RedisCluster != nil {
		RedisCluster.Close()
	}
	if DbConnection != nil {
		log.Info("Closing database connection.")
		DbConnection.Close()
	}
}
