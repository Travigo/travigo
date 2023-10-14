package events

import (
	"fmt"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/redis_client"
)

const numConsumers = 5

func StartConsumers() {
	// Run the background consumers
	log.Info().Msg("Starting events consumers")

	queue, err := redis_client.QueueConnection.OpenQueue("events")
	if err != nil {
		panic(err)
	}
	if err := queue.StartConsuming(numConsumers*200, 1*time.Second); err != nil {
		panic(err)
	}

	for i := 0; i < numConsumers; i++ {
		go startEventConsumer(queue, i)
	}
}
func startEventConsumer(queue rmq.Queue, id int) {
	log.Info().Msgf("Starting events consumer %d", id)

	if _, err := queue.AddBatchConsumer(fmt.Sprintf("event-queue-%d", id), 20, 2*time.Second, NewBatchConsumer(id)); err != nil {
		panic(err)
	}
}

type BatchConsumer struct {
	id int
}

func NewBatchConsumer(id int) *BatchConsumer {
	return &BatchConsumer{id: id}
}

func (consumer *BatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	for _, payload := range payloads {
		pretty.Println(string(payload))
	}

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume event")
		}
	}
}
