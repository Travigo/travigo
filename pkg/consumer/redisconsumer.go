package consumer

import (
	"fmt"
	"net/http"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/redis_client"
)

type RedisConsumer struct {
	QueueName string

	NumberConsumers int
	BatchSize       int

	Timeout time.Duration

	Consumer rmq.BatchConsumer
}

func (c *RedisConsumer) Setup() {
	c.startConsumers()
	c.startStatsServer()
}

func (c *RedisConsumer) startConsumers() {
	// Run the background consumers
	log.Info().Str("queue", c.QueueName).Msg("Starting consumers")

	queue, err := redis_client.QueueConnection.OpenQueue(c.QueueName)
	if err != nil {
		panic(err)
	}
	if err := queue.StartConsuming(int64(c.NumberConsumers*c.BatchSize), 1*time.Second); err != nil {
		panic(err)
	}

	for i := 0; i < c.NumberConsumers; i++ {
		go c.startQueueConsumer(queue, i)
	}
}
func (c *RedisConsumer) startQueueConsumer(queue rmq.Queue, id int) {
	log.Info().Msgf("Starting %s consumer %d", c.QueueName, id)

	if _, err := queue.AddBatchConsumer(fmt.Sprintf("event-queue-%d", id), int64(c.BatchSize), c.Timeout, c.Consumer); err != nil {
		panic(err)
	}
}

func (c *RedisConsumer) startStatsServer() {
	endpoint := fmt.Sprintf("/%s/stats", c.QueueName)
	http.Handle(endpoint, NewStatsHandler(redis_client.QueueConnection))
	http.Handle("/health", NewHealthHandler())

	log.Info().Msgf("Stats server listening on http://localhost:3333%s}", endpoint)
	if err := http.ListenAndServe(":3333", nil); err != nil {
		panic(err)
	}
}
