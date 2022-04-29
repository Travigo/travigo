package realtime

import (
	"time"

	"github.com/adjust/rmq/v4"
	"github.com/britbus/britbus/pkg/redis_client"
	"github.com/rs/zerolog/log"
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
	}
}
