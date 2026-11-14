package gtfs

import (
	"context"

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
	log.Info().Str("collection", b.Collection).Int("Length", len(batchItems)).Msg("Bulk write")
	_, err := collection.BulkWrite(context.Background(), batchItems, &options.BulkWriteOptions{})
	if err != nil {
		log.Fatal().Str("collection", b.Collection).Err(err).Msg("Failed to bulk write")
	}
}

func NewDatabaseBatchProcessingQueue(collection string, batchSize int) DatabaseBatchProcessingQueue {
	if batchSize <= 0 {
		batchSize = defaultBatchItems
	}
	return DatabaseBatchProcessingQueue{
		Collection: collection,
		BatchSize:  batchSize,
		items:      make(chan mongo.WriteModel, batchSize),
		done:       make(chan struct{}),
	}
}

type DatabaseBatchProcessingQueue struct {
	Collection string
	BatchSize  int

	items chan mongo.WriteModel
	done  chan struct{}
}

func (b *DatabaseBatchProcessingQueue) Add(item mongo.WriteModel) {
	b.items <- item
}

func (b *DatabaseBatchProcessingQueue) Process() {
	go func(b *DatabaseBatchProcessingQueue) {
		defer close(b.done)
		realtimeJourneysCollection := database.GetCollection(b.Collection)
		batchItems := make([]mongo.WriteModel, 0, b.BatchSize)
		for item := range b.items {
			batchItems = append(batchItems, item)
			if len(batchItems) >= b.BatchSize {
				flushBatch(realtimeJourneysCollection, b, batchItems)
				batchItems = make([]mongo.WriteModel, 0, b.BatchSize)
			}
		}
		if len(batchItems) > 0 {
			flushBatch(realtimeJourneysCollection, b, batchItems)
		}
	}(b)
}

func (b *DatabaseBatchProcessingQueue) Wait() {
	close(b.items)
	<-b.done
	log.Info().Str("collection", b.Collection).Msg("Finished processing queue")
}
