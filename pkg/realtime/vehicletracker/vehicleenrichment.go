package vehicletracker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider"
	"github.com/travigo/travigo/pkg/realtime/vehicledataprovider/dvlaves"
	"github.com/travigo/travigo/pkg/redis_client"
)

const (
	vehicleEnrichmentGroup     = "dvla-ves"
	vehicleEnrichmentTimeout   = 8 * time.Second
	vehicleEnrichmentClaimIdle = 20 * time.Second
)

// Redis acknowledgements do not check ownership. A paused worker must not
// remove a job after another worker has reclaimed it.
var acknowledgeVehicleEnrichmentScript = redis.NewScript(`
local pending = redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[2], ARGV[2], 1)
if #pending > 0 and pending[1][2] == ARGV[3] then
	redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
	return redis.call('XDEL', KEYS[1], ARGV[2])
end
return 0
`)

func startVehicleEnrichment(ctx context.Context) bool {
	apiKey := strings.TrimSpace(os.Getenv(dvlaves.APIKeyEnvironmentVariable))
	if apiKey == "" {
		return false
	}
	provider := dvlaves.New(apiKey, database.GetCollection(dvlaves.CollectionName), redis_client.Client)
	go func() {
		indexCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := provider.EnsureCacheIndex(indexCtx)
		cancel()
		if err != nil {
			log.Warn().Err(err).Msg("Failed to ensure DVLA VES cache expiry index")
		}
		host, _ := os.Hostname()
		for worker := range 2 {
			go consumeVehicleEnrichment(ctx, provider, fmt.Sprintf("%s-%d-%d", host, os.Getpid(), worker))
		}
	}()
	return true
}

func consumeVehicleEnrichment(ctx context.Context, provider vehicledataprovider.VehicleDataProvider, consumer string) {
	client := redis_client.Client
	stream := realtimestore.VehicleEnrichmentStream
	for ctx.Err() == nil {
		err := client.XGroupCreateMkStream(ctx, stream, vehicleEnrichmentGroup, "0").Err()
		if err == nil || strings.HasPrefix(err.Error(), "BUSYGROUP ") {
			err = readVehicleEnrichment(ctx, provider, consumer)
		}
		if ctx.Err() != nil {
			return
		}
		log.Warn().Err(err).Msg("Realtime vehicle enrichment queue unavailable")
		select {
		case <-time.After(time.Second):
		case <-ctx.Done():
		}
	}
}

func readVehicleEnrichment(ctx context.Context, provider vehicledataprovider.VehicleDataProvider, consumer string) error {
	client := redis_client.Client
	stream := realtimestore.VehicleEnrichmentStream
	cursor := "0-0"
	var nextReclaim time.Time
	for ctx.Err() == nil {
		if !time.Now().Before(nextReclaim) {
			messages, next, err := client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream: stream, Group: vehicleEnrichmentGroup, Consumer: consumer,
				MinIdle: vehicleEnrichmentClaimIdle, Start: cursor, Count: 1,
			}).Result()
			if err != nil {
				return err
			}
			cursor = next
			if cursor == "0-0" {
				nextReclaim = time.Now().Add(time.Second)
			}
			if len(messages) != 0 {
				if err := processVehicleEnrichment(ctx, provider, consumer, messages[0]); err != nil {
					return err
				}
				continue
			}
		}

		streams, err := client.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group: vehicleEnrichmentGroup, Consumer: consumer, Streams: []string{stream, ">"},
			Count: 1, Block: time.Second,
		}).Result()
		if errors.Is(err, redis.Nil) {
			continue
		}
		if err != nil {
			return err
		}
		for _, batch := range streams {
			for _, message := range batch.Messages {
				if err := processVehicleEnrichment(ctx, provider, consumer, message); err != nil {
					return err
				}
			}
		}
	}
	return ctx.Err()
}

func processVehicleEnrichment(ctx context.Context, provider vehicledataprovider.VehicleDataProvider, consumer string, message redis.XMessage) error {
	workCtx, cancel := context.WithTimeout(ctx, vehicleEnrichmentTimeout)
	defer cancel()
	err := enrichVehicle(workCtx, provider, message)
	if err != nil {
		log.Debug().Err(err).Msg("Realtime vehicle enrichment failed")
	}
	// Keep unfinished work pending so another worker can reclaim it after
	// shutdown or a dependency failure that can be resolved without a new event.
	if ctx.Err() != nil || errors.Is(err, vehicledataprovider.ErrLookupDeferred) {
		return ctx.Err()
	}
	return acknowledgeVehicleEnrichmentScript.Run(ctx, redis_client.Client,
		[]string{realtimestore.VehicleEnrichmentStream}, vehicleEnrichmentGroup, message.ID, consumer).Err()
}

func enrichVehicle(ctx context.Context, provider vehicledataprovider.VehicleDataProvider, message redis.XMessage) error {
	identifier, _ := message.Values["journey"].(string)
	registration, _ := message.Values["registration"].(string)
	created, _ := message.Values["created"].(string)
	createdAt, err := time.Parse(time.RFC3339Nano, created)
	if err != nil || identifier == "" || registration == "" {
		return errors.New("invalid vehicle enrichment job")
	}

	pending, err := realtimestore.VehicleEnrichmentPending(ctx, identifier, createdAt, registration)
	if err != nil {
		return fmt.Errorf("%w: %w", vehicledataprovider.ErrLookupDeferred, err)
	}
	if !pending {
		return nil
	}

	details, err := provider.GetVehicleDataBus(ctx, registration)
	if details != nil {
		if writeErr := realtimestore.UpdateBusDetails(ctx, identifier, createdAt, *details); writeErr != nil {
			return fmt.Errorf("%w: %w", vehicledataprovider.ErrLookupDeferred, writeErr)
		}
	}
	return err
}
