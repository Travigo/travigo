package gtfs

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const defaultBatchItems = 5000

func flushBatch(collection *mongo.Collection, b *DatabaseBatchProcessingQueue, batchItems []mongo.WriteModel) {
	// Performance Review [Set O] [PR030] [P2] (batch): BatchSize limits record count, not retained
	// BSON bytes: thousands of long journeys or tracks can fill both the channel
	// and active batch. Add a byte budget and size/time flush thresholds, propagate
	// cancellation/write errors to the importer, and bound database concurrency.
	// Consider unordered writes only after proving keys within a batch are distinct;
	// preserve ordering for repeated upserts to the same journey/track identifier.
	b.lastItemProcessed = time.Now()
	log.Info().Str("collection", b.Collection).Int("Length", len(batchItems)).Msg("Bulk write")
	_, err := collection.BulkWrite(context.Background(), batchItems, &options.BulkWriteOptions{})
	if err != nil {
		log.Fatal().Str("collection", b.Collection).Err(err).Msg("Failed to bulk write")
	}
}

func NewDatabaseBatchProcessingQueue(collection string, batchTimeout time.Duration, emptyTimeout time.Duration, batchSize int) DatabaseBatchProcessingQueue {
	if batchSize <= 0 {
		batchSize = defaultBatchItems
	}
	return DatabaseBatchProcessingQueue{
		Collection:        collection,
		BatchTimeout:      batchTimeout,
		EmptyTimeout:      emptyTimeout,
		BatchSize:         batchSize,
		items:             make(chan mongo.WriteModel, batchSize),
		lastItemProcessed: time.Now(),
	}
}

type DatabaseBatchProcessingQueue struct {
	Collection   string
	BatchTimeout time.Duration
	EmptyTimeout time.Duration
	BatchSize    int

	items             chan (mongo.WriteModel)
	lastItemProcessed time.Time
	ticker            *time.Ticker
}

func (b *DatabaseBatchProcessingQueue) Add(item mongo.WriteModel) {
	b.items <- item
}

func (b *DatabaseBatchProcessingQueue) Process() {
	go func(b *DatabaseBatchProcessingQueue) {
		realtimeJourneysCollection := database.GetCollection(b.Collection)

		b.ticker = time.NewTicker(b.BatchTimeout)

		for range b.ticker.C {
			batchItems := []mongo.WriteModel{}

			running := true

			for running {
				select {
				case i := <-b.items:
					batchItems = append(batchItems, i)

					if len(batchItems) >= b.BatchSize {
						flushBatch(realtimeJourneysCollection, b, batchItems)
						batchItems = []mongo.WriteModel{}
					}
				default: // Stop when no more values in chInternal
					running = false
				}
			}

			if len(batchItems) > 0 {
				flushBatch(realtimeJourneysCollection, b, batchItems)
			}
		}
	}(b)
}

func (b *DatabaseBatchProcessingQueue) Wait() {
	// Performance Review [Set H] [PR031] [P2]: Polling for inactivity adds fixed delays between
	// stages, and lastItemProcessed is set before BulkWrite finishes, so it is not
	// a completion signal. Stopping the ticker leaves the worker blocked on its
	// channel. Close producer input, drain/flush the final batch, then signal actual
	// completion and exit the worker explicitly to remove delays and blocked workers.
	waiting := true

	for waiting {
		now := time.Now()

		if now.Sub(b.lastItemProcessed) > b.EmptyTimeout {
			log.Info().Str("collection", b.Collection).Msg("Nothing left to process in queue")
			b.ticker.Stop()
			return
		}
		time.Sleep(5 * time.Second)
	}
}
