package tflarrivals

import (
	"encoding/json"

	"github.com/rs/zerolog/log"

	"github.com/adjust/rmq/v5"
)

type BusBatchConsumer struct {
}

func NewBusBatchConsumer() *BusBatchConsumer {
	return &BusBatchConsumer{}
}

type BusMonitorEvent struct {
	Line                     string
	DirectionRef             string
	NumberPlate              string
	OriginRef                string
	DestinationRef           string
	OriginAimedDepartureTime string
}

func (c *BusBatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	for _, payload := range payloads {
		var event BusMonitorEvent
		err := json.Unmarshal([]byte(payload), &event)

		if err != nil {
			continue
		}

		log.Info().Interface("event", event).Msg("Received event")

	}

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume from queue")
		}
	}
}
