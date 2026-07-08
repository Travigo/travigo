package vehicletracker

import (
	"context"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
)

func StartCleaner() {
	cleaner := rmq.NewCleaner(redis_client.QueueConnection)

	log.Info().Msg("Starting realtime_queue cleaner process")

	for range time.Tick(5 * time.Minute) {
		returned, err := cleaner.Clean()
		if err != nil {
			log.Error().Err(err).Msg("Failed to clean")
			continue
		}

		if returned != 0 {
			log.Info().Msgf("Cleaned %d records", returned)
		}

		removedLocations, err := realtimestore.CleanupStaleLocationIndex(context.Background())
		if err != nil {
			log.Error().Err(err).Msg("Failed to clean stale realtime location index entries")
			continue
		}
		if removedLocations != 0 {
			log.Info().Int64("locations", removedLocations).Msg("Cleaned stale realtime location index entries")
		}
	}
}
