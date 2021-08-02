package config

import (
	"context"

	"github.com/go-redis/redis/v8"
	"github.com/jmoiron/sqlx"
)

func InitCache() (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     AwsElasticCache,
		Password: "",
		DB:       0,
	})

	_, err := client.Ping(context.Background()).Result()
	if err != nil {
		return nil, err
	}

	return client, nil
}

func InitDBConnection(connectionString string) (*sqlx.DB, error) {
	client, err := sqlx.Open("pgx", connectionString)
	if err != nil {
		return nil, err
	}

	err = client.Ping()
	if err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}
