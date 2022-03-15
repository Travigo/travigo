package redis_client

import (
	"strconv"

	"github.com/britbus/britbus/pkg/util"
	"github.com/go-redis/redis/v8"
)

var C *redis.Client

const defaultConnectionAddress = "localhost:6379"
const defaultConnectionPassword = "localhost:6379"
const defaultDatabase = 0

func Connect() error {
	address := defaultConnectionAddress
	// password := defaultConnectionPassword
	database := defaultDatabase

	env := util.GetEnvironmentVariables()

	if env["BRITBUS_REDIS_ADDRESS"] != "" {
		address = env["BRITBUS_REDIS_ADDRESS"]
	}

	// if env["BRITBUS_REDIS_PASSWORD"] != "" {
	// 	password = env["BRITBUS_REDIS_PASSWORD"]
	// }

	if env["BRITBUS_REDIS_DATABASE"] != "" {
		if n, err := strconv.Atoi(env["BRITBUS_REDIS_DATABASE"]); err == nil {
			database = n
		} else {
			return err
		}
	}

	C = redis.NewClient(&redis.Options{
		Addr: address,
		// Password: password,
		DB: database,
	})

	return nil
}
