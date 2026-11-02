package redis_client

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/util"
)

var Client *redis.Client
var QueueConnection rmq.Connection

var eventsQueueMu sync.Mutex
var eventsQueue rmq.Queue

var ErrQueueConnectionNotInitialized = errors.New("queue connection is not initialized")

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

	Client.AddHook(realtimeMetricsHook{})
	statusCmd := Client.Ping(context.Background())
	err := statusCmd.Err()
	if err != nil {
		return err
	}

	queueConnection, err := rmq.OpenConnectionWithRedisClient("travigo", Client, nil)

	if err != nil {
		return err
	}

	eventsQueueMu.Lock()
	QueueConnection = queueConnection
	eventsQueue = nil
	eventsQueueMu.Unlock()

	return nil
}

// GetEventsQueue lazily opens the events queue once for the current connection.
func GetEventsQueue() (rmq.Queue, error) {
	eventsQueueMu.Lock()
	defer eventsQueueMu.Unlock()

	if QueueConnection == nil {
		return nil, ErrQueueConnectionNotInitialized
	}

	if eventsQueue != nil {
		return eventsQueue, nil
	}

	queue, err := QueueConnection.OpenQueue("events-queue")
	if err != nil {
		return nil, err
	}

	eventsQueue = queue
	return eventsQueue, nil
}

// Commands include rmq and all other traffic on the shared client; pipelines
// count their individual commands rather than pretending to be one operation.
type realtimeMetricsHook struct{}

func (realtimeMetricsHook) DialHook(next redis.DialHook) redis.DialHook {
	return func(ctx context.Context, network, addr string) (net.Conn, error) { return next(ctx, network, addr) }
}
func (realtimeMetricsHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmd)
		telemetry.Add(telemetry.RedisCommands, 1)
		telemetry.Observe(telemetry.Redis, time.Since(start))
		if err != nil && !errors.Is(err, redis.Nil) {
			telemetry.Add(telemetry.RedisErrors, 1)
		}
		return err
	}
}
func (realtimeMetricsHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		start := time.Now()
		err := next(ctx, cmds)
		telemetry.Add(telemetry.RedisCommands, int64(len(cmds)))
		telemetry.Observe(telemetry.Redis, time.Since(start))
		for _, cmd := range cmds {
			if cmd.Err() != nil && !errors.Is(cmd.Err(), redis.Nil) {
				telemetry.Add(telemetry.RedisErrors, 1)
			}
		}
		return err
	}
}
