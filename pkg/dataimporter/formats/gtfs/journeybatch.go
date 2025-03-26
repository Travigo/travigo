package gtfs

import (
	"context"
	"time"
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

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
	itemsWriteLock    sync.WaitGroup
	lastItemProcessed time.Time
	ticker            *time.Ticker
}

func (b *DatabaseBatchProcessingQueue) Add(item mongo.WriteModel) {
	lastItemProcessed.Wait()
	b.items <- item
}

func (b *DatabaseBatchProcessingQueue) Process() {
	go func(b *DatabaseBatchProcessingQueue) {
		realtimeJourneysCollection := database.GetCollection(b.Collection)

		b.ticker = time.NewTicker(b.BatchTimeout)

		for range b.ticker.C {
			batchItems := []mongo.WriteModel{}


			itemsWriteLock.Add(1)
			running := true

			for running {
				select {
				case i := <-b.items:
					batchItems = append(batchItems, i)
				default: // Stop when no more values in chInternal
					running = false
				}
			}

			if len(batchItems) > 0 {
				b.lastItemProcessed = time.Now()
				log.Info().Str("collection", b.Collection).Int("Length", len(batchItems)).Msg("Bulk write")
				_, err := realtimeJourneysCollection.BulkWrite(context.Background(), batchItems, &options.BulkWriteOptions{})
				if err != nil {
					log.Fatal().Str("collection", b.Collection).Err(err).Msg("Failed to bulk write")
				}
			}

			itemsWriteLock.Done()
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
