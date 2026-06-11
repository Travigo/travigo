package vehicletracker

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	realtimeMongoFlushInterval = time.Second
	realtimeMongoFlushSize     = 1000
	realtimeMongoQueueSize     = 10000
)

var (
	realtimeMongoFlushQueue chan mongo.WriteModel
	realtimeMongoFlushOnce  sync.Once
)

func StartRealtimeMongoFlusher() {
	realtimeMongoFlushOnce.Do(func() {
		realtimeMongoFlushQueue = make(chan mongo.WriteModel, realtimeMongoQueueSize)

		go runRealtimeMongoFlusher()
	})
}

func enqueueRealtimeJourneyOperations(operations []mongo.WriteModel) {
	if len(operations) == 0 {
		return
	}

	if realtimeMongoFlushQueue == nil {
		writeRealtimeJourneyOperations(operations)
		return
	}

	for _, operation := range operations {
		realtimeMongoFlushQueue <- operation
	}
}

func runRealtimeMongoFlusher() {
	ticker := time.NewTicker(realtimeMongoFlushInterval)
	defer ticker.Stop()

	var pending []mongo.WriteModel

	for {
		select {
		case operation := <-realtimeMongoFlushQueue:
			pending = append(pending, operation)
			if len(pending) >= realtimeMongoFlushSize {
				writeRealtimeJourneyOperations(pending)
				pending = nil
			}
		case <-ticker.C:
			if len(pending) > 0 {
				writeRealtimeJourneyOperations(pending)
				pending = nil
			}
		}
	}
}

func writeRealtimeJourneyOperations(operations []mongo.WriteModel) {
	if len(operations) == 0 {
		return
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")

	startTime := time.Now()
	_, err := realtimeJourneysCollection.BulkWrite(context.Background(), operations, options.BulkWrite().SetOrdered(false))
	log.Info().Int("Length", len(operations)).Str("Time", time.Since(startTime).String()).Msg("Bulk write realtime_journeys")

	if err != nil {
		log.Fatal().Err(err).Msg("Failed to bulk write Realtime Journeys")
	}
}
