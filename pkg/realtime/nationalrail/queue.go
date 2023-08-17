package nationalrail

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type BatchProcessingQueue struct {
	Timeout time.Duration
	Items   chan (mongo.WriteModel)
}

func (b *BatchProcessingQueue) Add(item mongo.WriteModel) {
	b.Items <- item
}

func (b *BatchProcessingQueue) Process() {
	go func(b *BatchProcessingQueue) {
		realtimeJourneysCollection := database.GetCollection("realtime_journeys")

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
