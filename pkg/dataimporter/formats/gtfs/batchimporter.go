package gtfs

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// PERF(medium-risk): upper bound on how many write models we accumulate in memory
// before forcing a flush, so a large backlog can't grow the batch slice / BulkWrite
// payload without limit.
const maxBatchItems = 10000

// flushBatch writes a batch of accumulated write models and records the flush time.
// Extracted so the accumulation loop can flush both at the size cap and at end of tick
// without duplicating the BulkWrite logic.
func flushBatch(collection *mongo.Collection, b *DatabaseBatchProcessingQueue, batchItems []mongo.WriteModel) {
	b.lastItemProcessed = time.Now()
	log.Info().Str("collection", b.Collection).Int("Length", len(batchItems)).Msg("Bulk write")
	_, err := collection.BulkWrite(context.Background(), batchItems, &options.BulkWriteOptions{})
	if err != nil {
		log.Fatal().Str("collection", b.Collection).Err(err).Msg("Failed to bulk write")
	}
}

func NewDatabaseBatchProcessingQueue(collection string, batchTimeout time.Duration, emptyTimeout time.Duration, batchSize int) DatabaseBatchProcessingQueue {
	return DatabaseBatchProcessingQueue{
		Collection:        collection,
		BatchTimeout:      batchTimeout,
		EmptyTimeout:      emptyTimeout,
		items:             make(chan mongo.WriteModel, batchSize),
		lastItemProcessed: time.Now(),
	}
}

type DatabaseBatchProcessingQueue struct {
	Collection   string
	BatchTimeout time.Duration
	EmptyTimeout time.Duration

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

					// PERF(medium-risk): cap the in-memory batch size. Previously a
					// single tick drained the entire channel into one slice with no
					// upper bound, so a large backlog could grow batchItems (and the
					// BulkWrite payload) without limit. When we hit the cap we flush
					// immediately and keep draining the rest of the channel in further
					// batches within the same tick. No items are dropped - they are
					// just written in bounded chunks instead of one unbounded slice.
					if len(batchItems) >= maxBatchItems {
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
