package redis_client

import (
	"context"
	"strconv"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/util"
	"github.com/go-redis/redis/v8"
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

	if env["BRITBUS_REDIS_ADDRESS"] != "" {
		address = env["BRITBUS_REDIS_ADDRESS"]
	}

	if env["BRITBUS_REDIS_PASSWORD"] != "" {
		password = env["BRITBUS_REDIS_PASSWORD"]
	}

	if env["BRITBUS_REDIS_DATABASE"] != "" {
		if n, err := strconv.Atoi(env["BRITBUS_REDIS_DATABASE"]); err == nil {
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

	QueueConnection, err = rmq.OpenConnectionWithRedisClient("britbus", Client, nil)

	if err != nil {
		return err
	}

	return nil
}
