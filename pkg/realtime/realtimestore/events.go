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
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyCreated,
			Timestamp: timestamp,
			Body:      *current,
		})
	} else if current.Cancelled && !previous.Cancelled {
		log.Info().Str("id", current.PrimaryIdentifier).Msg("RealtimeJourney has been cancelled")
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyCancelled,
			Timestamp: timestamp,
			Body:      *current,
		})
	}
	if current.SuppressFromDepartures && (previous == nil || !previous.SuppressFromDepartures || previous.ReplacedByJourneyRef != current.ReplacedByJourneyRef) {
		log.Info().
			Str("id", current.PrimaryIdentifier).
			Str("replaced_by", current.ReplacedByJourneyRef).
			Msg("RealtimeJourney overlay has been created")
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyOverlayCreated,
			Timestamp: timestamp,
			Body:      *current,
		})
	}

	if previous == nil {
		return events
	}

	if !previous.ActivelyTracked && current.ActivelyTracked {
		log.Info().Str("id", current.PrimaryIdentifier).Msg("RealtimeJourney is now actively tracked")
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyActivelyTracked,
			Timestamp: timestamp,
			Body:      *current,
		})
	}

	if previous.VehicleLocationDescription != current.VehicleLocationDescription {
		log.Info().Str("id", current.PrimaryIdentifier).Msg("RealtimeJourney location description changed")
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyLocationTextChanged,
			Timestamp: timestamp,
			Body:      *current,
		})
	}

	if previous.NextStopRef != current.NextStopRef || previous.NextStopIndex != current.NextStopIndex {
		log.Info().
			Str("id", current.PrimaryIdentifier).
			Str("previous_next_stop", previous.NextStopRef).
			Str("next_stop", current.NextStopRef).
			Msg("RealtimeJourney next stop changed")

		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyNextStopChanged,
			Timestamp: timestamp,
			Body:      *current,
		})
	}

	previousDelay, _ := realtimeJourneyDelay(previous)
	currentDelay, delayedStop := realtimeJourneyDelay(current)
	if currentDelay > 0 && currentDelay > previousDelay {
		log.Info().Str("id", current.PrimaryIdentifier).Dur("delay", currentDelay).Msg("RealtimeJourney delay increased")
		events = append(events, ctdf.Event{
			Type:      ctdf.EventTypeRealtimeJourneyDelayed,
			Timestamp: timestamp,
			Body: map[string]interface{}{
				"RealtimeJourney": *current,
				"DelaySeconds":    int(currentDelay / time.Second),
				"Stop":            delayedStop,
			},
		})
	}

	for id, currentStop := range current.Stops {
		if currentStop == nil || currentStop.TimeType == ctdf.RealtimeJourneyStopTimeHistorical {
			continue
		}

		stopRef := currentStop.StopRef
		if stopRef == "" {
			stopRef = id
		}
		previousStop := previous.RealtimeStop(stopRef, currentStop.JourneyStopIndex)
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
					"RealtimeJourney":  *current,
					"Stop":             stopRef,
					"JourneyStopIndex": currentStop.JourneyStopIndex,
					"NewPlatform":      newPlatform,
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
					"RealtimeJourney":  *current,
					"Stop":             stopRef,
					"JourneyStopIndex": currentStop.JourneyStopIndex,
					"OldPlatform":      oldPlatform,
					"NewPlatform":      newPlatform,
				},
			})
		}
	}

	return events
}

func realtimeJourneyDelay(realtimeJourney *ctdf.RealtimeJourney) (time.Duration, string) {
	if realtimeJourney == nil || realtimeJourney.Journey == nil {
		return 0, ""
	}
	maximumDelay := time.Duration(0)
	delayedStop := ""
	for pathIndex, path := range realtimeJourney.Journey.Path {
		if path == nil {
			continue
		}
		for _, candidate := range []struct {
			stopRef   string
			stopIndex int
			scheduled time.Time
			actual    time.Time
		}{
			{stopRef: path.OriginStopRef, stopIndex: pathIndex, scheduled: path.OriginDepartureTime, actual: realtimeStopDepartureTime(realtimeJourney, path.OriginStopRef, pathIndex)},
			{stopRef: path.DestinationStopRef, stopIndex: pathIndex + 1, scheduled: path.DestinationArrivalTime, actual: realtimeStopArrivalTime(realtimeJourney, path.DestinationStopRef, pathIndex+1)},
		} {
			if candidate.actual.IsZero() || candidate.scheduled.IsZero() {
				continue
			}
			scheduled := scheduledJourneyTime(realtimeJourney.JourneyRunDate, candidate.scheduled, candidate.actual.Location())
			delay := candidate.actual.Sub(scheduled)
			if delay > maximumDelay {
				maximumDelay, delayedStop = delay, candidate.stopRef
			}
		}
	}
	return maximumDelay, delayedStop
}

func realtimeStopDepartureTime(realtimeJourney *ctdf.RealtimeJourney, stopRef string, stopIndex int) time.Time {
	if stop := realtimeJourney.RealtimeStop(stopRef, stopIndex); stop != nil {
		return stop.DepartureTime
	}
	return time.Time{}
}

func realtimeStopArrivalTime(realtimeJourney *ctdf.RealtimeJourney, stopRef string, stopIndex int) time.Time {
	if stop := realtimeJourney.RealtimeStop(stopRef, stopIndex); stop != nil {
		return stop.ArrivalTime
	}
	return time.Time{}
}

func scheduledJourneyTime(runDate time.Time, scheduled time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.Local
	}
	if runDate.IsZero() {
		runDate = scheduled
	}
	result := time.Date(runDate.In(location).Year(), runDate.In(location).Month(), runDate.In(location).Day(), scheduled.Hour(), scheduled.Minute(), scheduled.Second(), scheduled.Nanosecond(), location)
	return result
}
