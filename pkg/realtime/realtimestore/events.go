package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

func previousRealtimeJourneyForEvents(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, bool) {
	if identifier == "" {
		return nil, false
	}

	result := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if errors.Is(result.Err(), redis.Nil) {
		return nil, true
	}
	if result.Err() != nil {
		log.Error().Err(result.Err()).Str("id", identifier).Msg("Failed to load previous realtime journey for events")
		return nil, false
	}

	realtimeJourney, err := decodeStoredRealtimeJourney(ctx, []byte(result.Val()), false)
	if err != nil {
		log.Error().Err(err).Str("id", identifier).Msg("Failed to decode previous realtime journey for events")
		return nil, false
	}

	return realtimeJourney, true
}

func publishRealtimeJourneyEvents(previous *ctdf.RealtimeJourney, current *ctdf.RealtimeJourney, previousKnown bool) {
	events := realtimeJourneyEvents(previous, current, previousKnown, time.Now())
	if len(events) == 0 {
		return
	}

	if redis_client.QueueConnection == nil {
		log.Warn().Str("id", current.PrimaryIdentifier).Int("events", len(events)).Msg("Skipping realtime journey events because queue connection is not initialized")
		return
	}

	eventQueue, err := redis_client.QueueConnection.OpenQueue("events-queue")
	if err != nil {
		log.Error().Err(err).Str("id", current.PrimaryIdentifier).Msg("Failed to open events queue")
		return
	}

	payloads := make([][]byte, 0, len(events))
	for _, event := range events {
		eventBytes, err := json.Marshal(event)
		if err != nil {
			log.Error().Err(err).Str("id", current.PrimaryIdentifier).Str("type", string(event.Type)).Msg("Failed to encode realtime journey event")
			continue
		}

		payloads = append(payloads, eventBytes)
	}

	if len(payloads) == 0 {
		return
	}

	if err := eventQueue.PublishBytes(payloads...); err != nil {
		log.Error().Err(err).Str("id", current.PrimaryIdentifier).Int("events", len(payloads)).Msg("Failed to publish realtime journey events")
	}
}

func realtimeJourneyEvents(previous *ctdf.RealtimeJourney, current *ctdf.RealtimeJourney, previousKnown bool, timestamp time.Time) []ctdf.Event {
	if !previousKnown || current == nil || current.PrimaryIdentifier == "" {
		return nil
	}

	events := []ctdf.Event{}

	if previous == nil {
		log.Info().Str("id", current.PrimaryIdentifier).Msg("RealtimeJourney has been created")
		return append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyCreated,
			Timestamp: timestamp,
			Body:      *current,
		})
	}

	if current.Cancelled && !previous.Cancelled {
		log.Info().Str("id", current.PrimaryIdentifier).Msg("RealtimeJourney has been cancelled")
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyCancelled,
			Timestamp: timestamp,
			Body:      *current,
		})
	}

	for id, currentStop := range current.Stops {
		if currentStop == nil || currentStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
			continue
		}

		previousStop := previous.Stops[id]
		if previousStop == nil {
			continue
		}

		oldPlatform := previousStop.Platform
		newPlatform := currentStop.Platform

		if oldPlatform == "" && newPlatform != oldPlatform {
			log.Info().
				Str("id", current.PrimaryIdentifier).
				Str("platform", newPlatform).
				Msg("RealtimeJourney stop platform set")

			events = append(events, ctdf.Event{
				Type:      ctdf.EventTypeRealtimeJourneyPlatformSet,
				Timestamp: timestamp,
				Body: map[string]interface{}{
					"RealtimeJourney": *current,
					"Stop":            id,
					"NewPlatform":     newPlatform,
				},
			})
		} else if oldPlatform != "" && newPlatform != oldPlatform {
			log.Info().
				Str("id", current.PrimaryIdentifier).
				Str("oldplatform", oldPlatform).
				Str("newplatform", newPlatform).
				Msg("RealtimeJourney stop platform changed")

			events = append(events, ctdf.Event{
				Type:      ctdf.EventTypeRealtimeJourneyPlatformChanged,
				Timestamp: timestamp,
				Body: map[string]interface{}{
					"RealtimeJourney": *current,
					"Stop":            id,
					"OldPlatform":     oldPlatform,
					"NewPlatform":     newPlatform,
				},
			})
		}
	}

	return events
}
