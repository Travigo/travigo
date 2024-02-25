package gtfs

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type JourneysBatchProcessingQueue struct {
	Timeout time.Duration
	Items   chan (mongo.WriteModel)

	LastItemProcessed time.Time
}

func (b *JourneysBatchProcessingQueue) Add(item mongo.WriteModel) {
	b.Items <- item
}

func (b *JourneysBatchProcessingQueue) Process() {
	go func(b *JourneysBatchProcessingQueue) {
		realtimeJourneysCollection := database.GetCollection("journeys_gtfs")

		ticker := time.NewTicker(b.Timeout)

		for range ticker.C {
			batchItems := []mongo.WriteModel{}

			running := true

			for running {
				select {
				case i := <-b.Items:
					batchItems = append(batchItems, i)
				default: // Stop when no more values in chInternal
					running = false
				}
			}

			if len(batchItems) > 0 {
				log.Info().Int("Length", len(batchItems)).Msg("Bulk write")
				_, err := realtimeJourneysCollection.BulkWrite(context.TODO(), batchItems, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Err(err).Msg("Failed to bulk write Journeys")
				}
			}
		}
	}(b)
}

func (b *JourneysBatchProcessingQueue) Wait() {
	waiting := true

	for waiting {
		now := time.Now()

		if now.Sub(b.LastItemProcessed) > 5*b.Timeout {
			log.Info().Msg("Nothing left to process in queue")
			return
		}
		time.Sleep(5 * time.Second)
	}
}
