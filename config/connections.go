package config

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"
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
	options := &redis.ClusterOptions{
		Addrs:       strings.Split(Conf.RedisConnectionString, ";"),
		PoolTimeout: 10 * time.Second,
	}

	if Conf.RedisRemote {
		options.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	RedisCluster = redis.NewClusterClient(options)

	_, err := RedisCluster.Ping(context.Background()).Result()
	if err != nil {
		log.WithError(err).Errorf("Failed to ping redis cluster connection")
		return err
	}

	clusterInitDuration := time.Minute
	clusterInitStartTime := time.Now()
	for {
		res, err := RedisCluster.ClusterInfo(context.Background()).Result()
		if err != nil {
			if time.Since(clusterInitStartTime) > clusterInitDuration {
				log.WithError(err).Error("Failed to init redis cluster")
				return err
			}
		} else {
			if strings.Contains(res, "cluster_state:ok") {
				time.Sleep(5 * time.Second)
				log.Debug("Redis cluster connection initialized")
				break
			}

			err = fmt.Errorf("cluster state is not ok")
		}

		log.WithError(err).Trace("Redis cluster init error, retrying...")
		time.Sleep(time.Millisecond * 100)
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
