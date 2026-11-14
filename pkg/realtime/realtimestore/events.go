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

	eventQueue, err := redis_client.GetEventsQueue()
	if errors.Is(err, redis_client.ErrQueueConnectionNotInitialized) {
		log.Warn().Str("id", current.PrimaryIdentifier).Int("events", len(events)).Msg("Skipping realtime journey events because queue connection is not initialized")
		return
	}
	if err != nil {
		log.Error().Err(err).Str("id", current.PrimaryIdentifier).Msg("Failed to open events queue")
		return
	}
	replaceRealtimeJourneyEventBodies(events, notificationJourney(current))
	payloads := make([][]byte, 0, len(events))
	for _, event := range events {
		eventBytes, err := json.Marshal(event)
		if err != nil {
			log.Error().Err(err).Str("id", current.PrimaryIdentifier).Str("type", string(event.Type)).Msg("Failed to encode realtime journey event")
			continue
		}

		if len(eventBytes) == 0 {
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

func replaceRealtimeJourneyEventBodies(events []ctdf.Event, current realtimeJourneyEventBody) {
	for index := range events {
		switch body := events[index].Body.(type) {
		case ctdf.RealtimeJourney:
			events[index].Body = current
		case map[string]interface{}:
			if _, ok := body["RealtimeJourney"]; !ok {
				continue
			}
			body["RealtimeJourney"] = current
		}
	}
}

// Queue snapshots retain the JSON fields used by notification filters,
// messages and links. Static geometry and full stop documents stay out of them.
type realtimeJourneyEventBody struct {
	PrimaryIdentifier          string
	Journey                    *notificationSchedule
	JourneyRunDate             time.Time
	VehicleLocationDescription string
	NextStopRef                string
	NextStopIndex              int
	NextStop                   *notificationStop
	ReplacedByJourneyRef       string
}

type notificationSchedule struct {
	PrimaryIdentifier  string
	DepartureTime      time.Time
	DestinationDisplay string
	Path               []notificationPath
}

type notificationPath struct {
	OriginStopRef string
}

type notificationStop struct {
	PrimaryName string
}

func notificationJourney(current *ctdf.RealtimeJourney) realtimeJourneyEventBody {
	body := realtimeJourneyEventBody{
		PrimaryIdentifier:          current.PrimaryIdentifier,
		JourneyRunDate:             current.JourneyRunDate,
		VehicleLocationDescription: current.VehicleLocationDescription,
		NextStopRef:                current.NextStopRef,
		NextStopIndex:              current.NextStopIndex,
		ReplacedByJourneyRef:       current.ReplacedByJourneyRef,
	}
	if current.Journey != nil {
		body.Journey = &notificationSchedule{
			PrimaryIdentifier:  current.Journey.PrimaryIdentifier,
			DepartureTime:      current.Journey.DepartureTime,
			DestinationDisplay: current.Journey.DestinationDisplay,
		}
		// Cancellation messages use the origin of the first leg.
		if len(current.Journey.Calls) > 0 {
			body.Journey.Path = []notificationPath{{OriginStopRef: current.Journey.Calls[0].StopRef}}
		}
	}
	if current.NextStop != nil {
		body.NextStop = &notificationStop{PrimaryName: current.NextStop.PrimaryName}
	}
	return body
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
	// Most maps are canonical. Allocate fallback metadata only on a miss.
	var indexed map[string]*ctdf.RealtimeJourneyStops
	var first, counts map[string]int
	lookup := func(ref string, index int) *ctdf.RealtimeJourneyStops {
		key := ctdf.RealtimeJourneyStopKey(ref, index)
		if stop := realtimeJourney.Stops[key]; stop != nil {
			return stop
		}
		if indexed == nil {
			indexed = map[string]*ctdf.RealtimeJourneyStops{}
			for storedKey, stop := range realtimeJourney.Stops {
				if stop == nil {
					continue
				}
				canonical := ctdf.RealtimeJourneyStopKey(stop.StopRef, stop.JourneyStopIndex)
				if storedKey != canonical {
					indexed[canonical] = stop
				}
			}
		}
		if stop := indexed[key]; stop != nil {
			return stop
		}
		legacy := realtimeJourney.Stops[ref]
		if legacy == nil {
			return nil
		}
		if counts == nil {
			first, counts = map[string]int{}, map[string]int{}
			add := func(ref string, index int) {
				if counts[ref] == 0 {
					first[ref] = index
				}
				counts[ref]++
			}
			if len(realtimeJourney.Journey.Calls) > 0 {
				add(realtimeJourney.Journey.Calls[0].StopRef, 0)
			}
			index := 1
			for _, call := range realtimeJourney.Journey.LegCalls() {
				add(call.StopRef, index)
				index++
			}
		}
		if counts[ref] <= 1 || first[ref] == index {
			return legacy
		}
		return nil
	}

	departure := func(ref string, index int) time.Time {
		if stop := lookup(ref, index); stop != nil {
			return stop.DepartureTime
		}
		return time.Time{}
	}
	arrival := func(ref string, index int) time.Time {
		if stop := lookup(ref, index); stop != nil {
			return stop.ArrivalTime
		}
		return time.Time{}
	}

	pathIndex := 0
	for origin, destination := range realtimeJourney.Journey.LegCalls() {
		for _, candidate := range []struct {
			stopRef   string
			stopIndex int
			scheduled time.Time
			actual    time.Time
		}{
			{stopRef: origin.StopRef, stopIndex: pathIndex, scheduled: origin.Departure, actual: departure(origin.StopRef, pathIndex)},
			{stopRef: destination.StopRef, stopIndex: pathIndex + 1, scheduled: destination.Arrival, actual: arrival(destination.StopRef, pathIndex+1)},
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
		pathIndex++
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
