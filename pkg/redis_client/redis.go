package redis_client

import (
	"context"
	"github.com/adjust/rmq/v5"
	"strconv"

	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/util"
)

var Client *redis.Client
var QueueConnection rmq.Connection

const defaultConnectionAddress = "localhost:6379"
const defaultConnectionPassword = ""
const defaultDatabase = 0

func Connect() error {
	address := defaultConnectionAddress
	password := defaultConnectionPassword
	database := defaultDatabase

	env := util.GetEnvironmentVariables()

	if env["TRAVIGO_REDIS_ADDRESS"] != "" {
		address = env["TRAVIGO_REDIS_ADDRESS"]
	}

	if env["TRAVIGO_REDIS_PASSWORD"] != "" {
		password = env["TRAVIGO_REDIS_PASSWORD"]
	}

	if env["TRAVIGO_REDIS_DATABASE"] != "" {
		if n, err := strconv.Atoi(env["TRAVIGO_REDIS_DATABASE"]); err == nil {
			database = n
		} else {
			return err
		}
	}

	if password == "" {
		Client = redis.NewClient(&redis.Options{
			Addr: address,
			DB:   database,
		})
	} else {
		Client = redis.NewClient(&redis.Options{
			Addr:     address,
			Password: password,
			DB:       database,
		})
	}

	statusCmd := Client.Ping(context.Background())
	err := statusCmd.Err()
	if err != nil {
		return err
	}

	QueueConnection, err = rmq.OpenConnectionWithRedisClient("travigo", Client, nil)

	if err != nil {
		return err
	}

	return nil
}
