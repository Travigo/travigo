package vehicletracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"github.com/travigo/travigo/pkg/redis_client"
)

func (consumer *BatchConsumer) updateRealtimeJourney(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) error {
	err := consumer.tryUpdateRealtimeJourney(journeyID, vehicleUpdateEvent)
	if errors.Is(err, realtimestore.ErrRealtimeJourneyConflict) {
		// Only compare against the watermark of this feed's admitted stream.
		if vehicleUpdateEvent.LocalID != "" {
			key := realtimeIdentificationMappingKey(vehicleUpdateEvent.LocalID)
			latest, readErr := redis_client.Client.HGet(context.Background(), key, "last_updated").Int64()
			if readErr == nil && latest > vehicleUpdateEvent.RecordedAt.UnixNano() {
				return nil
			}
		}
		// Recompute against the winning write while this delivery is still held.
		return consumer.tryUpdateRealtimeJourney(journeyID, vehicleUpdateEvent)
	}
	return err
}

func (consumer *BatchConsumer) tryUpdateRealtimeJourney(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) error {
	ctx := context.Background()
	currentTime := vehicleUpdateEvent.RecordedAt
	if currentTime.IsZero() {
		currentTime = time.Now()
	}
	update := vehicleUpdateEvent.VehicleLocationUpdate
	if len(update.StopUpdates) == 0 && update.Location.Type == "" {
		return errors.New("realtime update has no stop predictions or location")
	}
	serviceDate, err := time.Parse("2006-01-02", update.Timeframe)
	if err != nil {
		return fmt.Errorf("invalid realtime timeframe %q: %w", update.Timeframe, err)
	}
	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, update.Timeframe, journeyID)
	snapshot, err := realtimestore.FindForTrackerUpdate(ctx, realtimeJourneyIdentifier)
	if err != nil {
		return err
	}
	realtimeJourney := snapshot.Journey
	realtimeJourneyReliability := ctdf.RealtimeJourneyReliabilityLocationWithoutTrack
	if len(update.StopUpdates) > 0 {
		realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityExternalProvided
	}

	newRealtimeJourney := false
	if realtimeJourney != nil && realtimeJourney.Journey == nil {
		log.Error().Msg("RealtimeJourney without a Journey found, deleting")
		// realtimeJourneysCollection.DeleteOne(context.Background(), searchQuery) TODO
		return errors.New("RealtimeJourney without a Journey found, deleting")
	}

	var historyKey string
	if update.Location.Type == "Point" {
		historyKey = vehicleHistoryKey(vehicleUpdateEvent)
	}
	cachedJourney, err := consumer.getCachedTrackedJourney(journeyID, currentTime)
	if err != nil {
		return err
	}
	journey := cachedJourney.Journey
	if journey == nil || len(journey.Calls) == 0 {
		return errors.New("tracked journey has no path")
	}
	journeyTimezone := consumer.loadLocation(journey.DepartureTimezone)
	if update.Location.Type == "Point" {
		journey = cachedJourney.journeyForMatching()
	}

	if realtimeJourney == nil {
		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier:      realtimeJourneyIdentifier,
			ActivelyTracked:        true,
			TimeoutDurationMinutes: 10,
			Journey:                journey,
			Service:                journey.Service,

			CreationDateTime: currentTime,

			VehicleRef: vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier,
			Stops:      map[string]*ctdf.RealtimeJourneyStops{},
		}
		newRealtimeJourney = true
	} else {
		realtimeJourney.Journey = journey
		realtimeJourney.Service = journey.Service
	}
	realtimeJourney.JourneyRunDate = serviceDate

	offset := realtimeJourney.Offset
	if len(update.StopUpdates) > 0 {
		offset = 0
	}
	var historyPosition journeyPositionMatch
	positionMatched := false
	if update.Location.Type == "Point" {
		historyPosition, positionMatched = matchJourneyPositionWithStopFallback(journey, update.Location)
	}

	journeyStopUpdates := map[string]*ctdf.RealtimeJourneyStops{}
	nextStopIndex := -1
	// GPS can generate estimates when the feed supplies no stop predictions.
	if len(update.StopUpdates) == 0 && positionMatched {
		pathIndex := historyPosition.PathIndex
		origin, destination := journey.LegEndpoints(pathIndex)
		nextStopIndex = pathIndex + 1
		if historyPosition.UsedGlobalTrack {
			realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithoutTrack
		} else {
			realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithTrack
		}
		destinationArrivalTimeWithDate := stopPredictionTime(destination.ArrivalTime(), time.Time{}, 0, serviceDate, journeyTimezone)
		originDepartureTimeWithDate := stopPredictionTime(origin.Departure, time.Time{}, 0, serviceDate, journeyTimezone)

		// How long it take to travel between origin & destination
		currentPathTraversalTime := destinationArrivalTimeWithDate.Sub(originDepartureTimeWithDate)

		// How far we are between origin & departure (% of journey path, NOT time or metres)
		// TODO: this is a hack, replace with actual distance
		currentPathPercentageComplete := historyPosition.LegProgress

		// Calculate what the expected time of the current position of the vehicle should be
		currentPathPositionExpectedTime := originDepartureTimeWithDate.Add(
			time.Duration(int(currentPathPercentageComplete * float64(currentPathTraversalTime.Nanoseconds()))))

		// Offset is how far behind or ahead the vehicle is from its positions expected time
		offset = currentTime.Sub(currentPathPositionExpectedTime).Round(10 * time.Second)

		// If the offset is too small then just turn it to zero so we can mark buses as on time
		if absDuration(offset) <= 45*time.Second {
			offset = time.Duration(0)
		}

		// Calculate all the estimated stop arrival & departure times. A vehicle
		// cannot leave a timed stop before its scheduled departure, so its delay
		// can be absorbed by scheduled dwell time before calculating later calls.
		if newRealtimeJourney || realtimeJourney.NextStopIndex != nextStopIndex || realtimeJourney.Offset != offset || futureStopsNeedRebuild(realtimeJourney, cachedJourney.StopKeys, pathIndex, offset, journeyTimezone) {
			for _, stopUpdate := range estimateFutureStops(realtimeJourney.Journey, pathIndex, offset) {
				stopUpdate.ArrivalTime = stopPredictionTime(stopUpdate.ArrivalTime, time.Time{}, 0, serviceDate, journeyTimezone)
				stopUpdate.DepartureTime = stopPredictionTime(stopUpdate.DepartureTime, time.Time{}, 0, serviceDate, journeyTimezone)
				journeyStopUpdates[ctdf.RealtimeJourneyStopKey(stopUpdate.StopRef, stopUpdate.JourneyStopIndex)] = stopUpdate
			}
		}
	} else {
		nextOccurrence := map[string]int{}
		for _, stopUpdate := range vehicleUpdateEvent.VehicleLocationUpdate.StopUpdates {
			occurrences := cachedJourney.StopOccurrences[stopUpdate.StopID]
			occurrence := nextOccurrence[stopUpdate.StopID]
			if occurrence >= len(occurrences) {
				continue
			}
			journeyStopIndex := occurrences[occurrence]
			nextOccurrence[stopUpdate.StopID] = occurrence + 1
			arrivalTime := stopUpdate.ArrivalTime
			departureTime := stopUpdate.DepartureTime
			call := journey.StopCall(journeyStopIndex)
			if call == nil {
				continue
			}
			scheduledArrivalTime, scheduledDepartureTime := call.Arrival, call.Departure
			if call.End {
				scheduledArrivalTime = call.ArrivalTime()
				scheduledDepartureTime = scheduledArrivalTime
			}

			arrivalTime = stopPredictionTime(arrivalTime, scheduledArrivalTime, stopUpdate.ArrivalOffset, serviceDate, journeyTimezone)
			departureTime = stopPredictionTime(departureTime, scheduledDepartureTime, stopUpdate.DepartureOffset, serviceDate, journeyTimezone)
			if (stopUpdate.DepartureTime.IsZero() || stopUpdate.DepartureTime.Year() == 1970) && arrivalTime.After(departureTime) && !departureTime.IsZero() {
				departureTime = arrivalTime
			}

			realtimeStop := &ctdf.RealtimeJourneyStops{
				StopRef:          stopUpdate.StopID,
				JourneyStopIndex: journeyStopIndex,
				TimeType:         ctdf.RealtimeJourneyStopTimeEstimatedFuture,

				ArrivalTime:   arrivalTime,
				DepartureTime: departureTime,
			}
			journeyStopUpdates[ctdf.RealtimeJourneyStopKey(realtimeStop.StopRef, realtimeStop.JourneyStopIndex)] = realtimeStop
		}

		if positionMatched {
			nextStopIndex = historyPosition.PathIndex + 1
		}
	}

	if realtimeJourney.Stops == nil {
		realtimeJourney.Stops = map[string]*ctdf.RealtimeJourneyStops{}
	}
	for key, stop := range journeyStopUpdates {
		realtimeJourney.Stops[key] = stop
	}
	if nextStopIndex < 0 && len(journeyStopUpdates) > 0 {
		inferred := nextStopAt(realtimeJourney, cachedJourney.StopOccurrences, serviceDate, journeyTimezone, currentTime)
		// Time estimates alone must not move a tracked run backwards.
		if realtimeJourney.NextStopRef == "" || inferred >= realtimeJourney.NextStopIndex {
			nextStopIndex = inferred
		}
	}
	if nextStopIndex >= 0 {
		realtimeJourney.NextStopIndex = nextStopIndex
		realtimeJourney.DepartedStopRef = ""
		if nextStopIndex == 0 {
			realtimeJourney.NextStopRef = journey.Calls[0].StopRef
		} else {
			origin, destination := journey.LegEndpoints(nextStopIndex - 1)
			realtimeJourney.DepartedStopRef = origin.StopRef
			realtimeJourney.NextStopRef = destination.StopRef
		}
	}

	realtimeJourney.ModificationDateTime = currentTime
	realtimeJourney.Occupancy = vehicleUpdateEvent.VehicleLocationUpdate.Occupancy
	realtimeJourney.Reliability = realtimeJourneyReliability
	realtimeJourney.VehicleRef = vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier
	realtimeJourney.DataSource = vehicleUpdateEvent.DataSource

	realtimeJourney.Offset = offset

	snapshot.Journey = realtimeJourney
	if update.Location.Type != "" {
		_ = snapshot.SetLocation(update.Location, update.Bearing, currentTime)
	}
	persistenceStarted := time.Now()
	saveErr := realtimestore.SaveRealtimeJourneySnapshot(ctx, snapshot)
	telemetry.Observe(telemetry.Persistence, time.Since(persistenceStarted))
	if saveErr != nil {
		// Consume still acknowledges failed deliveries after identification has
		// advanced its timestamp. Retrying dependency errors requires coordinating
		// the admission watermark with the commit.
		return saveErr
	}
	telemetry.CommittedEvent(currentTime)

	if historyKey != "" && historyPosition.HasTrackMatch {
		storeVehicleJourneyHistory(ctx, historyKey, currentTime, journeyID, historyPosition.JourneyProgress)
	}
	return nil
}

// GTFS omits absolute times for delay-only updates. Resolve those against the
// service day before adding delay, keeping zero times absent and 24h+ clocks.
func stopPredictionTime(value, scheduled time.Time, delaySeconds int, serviceDate time.Time, location *time.Location) time.Time {
	delay := time.Duration(0)
	if value.IsZero() || value.Year() == 1970 {
		value = scheduled
		delay = time.Duration(delaySeconds) * time.Second
	}
	if value.IsZero() {
		return time.Time{}
	}
	if value.Year() <= 1 {
		value = serviceTimeOnDate(serviceDate, value, location)
	}
	return value.Add(delay)
}

// Predictions describe a call as next until its departure, including dwell.
// If every usable time has passed, progress is uncertain and stays as stored.
func nextStopAt(realtimeJourney *ctdf.RealtimeJourney, occurrences map[string][]int, serviceDate time.Time, location *time.Location, observedAt time.Time) int {
	journey := realtimeJourney.Journey
	for index, call := range journey.StopCalls() {
		ref, arrival, departure := call.StopRef, call.Arrival, call.Departure
		arrival = stopPredictionTime(arrival, time.Time{}, 0, serviceDate, location)
		departure = stopPredictionTime(departure, time.Time{}, 0, serviceDate, location)
		stop := realtimeJourney.Stops[ctdf.RealtimeJourneyStopKey(ref, index)]
		// Existing records can still use stop-reference keys without an index.
		if stop == nil && len(occurrences[ref]) > 0 && occurrences[ref][0] == index {
			stop = realtimeJourney.Stops[ref]
		}
		if stop != nil {
			arrival = stopPredictionTime(stop.ArrivalTime, arrival, 0, serviceDate, location)
			departure = stopPredictionTime(stop.DepartureTime, departure, 0, serviceDate, location)
		}
		if call.End || arrival.After(departure) {
			departure = arrival
		}
		if departure.After(observedAt) {
			return index
		}
	}
	return -1
}

func journeyStopOccurrenceIndex(journey *ctdf.Journey, stopRef string, occurrence int) int {
	for index, call := range journey.StopCalls() {
		if call.StopRef == stopRef {
			if occurrence == 0 {
				return index
			}
			occurrence--
		}
	}
	return -1
}

func serviceTimeOnDate(serviceDate time.Time, serviceTime time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.Local
	}
	serviceDayStart := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 12, 0, 0, 0, location).Add(-12 * time.Hour)
	encodedStart := time.Date(0, time.January, 1, 0, 0, 0, 0, serviceTime.Location())
	return serviceDayStart.Add(serviceTime.Sub(encodedStart))
}

func estimateFutureStops(journey *ctdf.Journey, startIndex int, offset time.Duration) []*ctdf.RealtimeJourneyStops {
	stops := make([]*ctdf.RealtimeJourneyStops, 0, len(journey.Calls))
	index := -1
	for callIndex := 1; callIndex < len(journey.Calls); callIndex++ {
		call := &journey.Calls[callIndex]
		if call.Start {
			continue
		}
		index++
		if index < startIndex {
			continue
		}
		arrival, departure := estimatedCallTimes(journey, callIndex, &offset)
		stops = append(stops, &ctdf.RealtimeJourneyStops{
			StopRef: call.StopRef, JourneyStopIndex: index + 1,
			TimeType:    ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			ArrivalTime: arrival, DepartureTime: departure,
		})
	}
	return stops
}

func estimatedCallTimes(journey *ctdf.Journey, callIndex int, offset *time.Duration) (arrival, departure time.Time) {
	call := &journey.Calls[callIndex]
	arrival = call.ArrivalTime().Add(*offset).Round(time.Minute)
	if callIndex+1 < len(journey.Calls) {
		if call.End {
			call = &journey.Calls[callIndex+1]
		}
		scheduled := call.Departure
		departure = scheduled
		if arrival.After(departure) {
			departure = arrival
		}
		*offset = departure.Sub(scheduled)
	}
	return arrival, departure
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}

// Compare predictions without allocating a replacement slice. Recomputing the
// expected times also detects timetable edits and delay absorbed by dwell time.
func futureStopsNeedRebuild(journey *ctdf.RealtimeJourney, keys []string, start int, offset time.Duration, location *time.Location) bool {
	i := -1
	for callIndex := 1; callIndex < len(journey.Journey.Calls); callIndex++ {
		if journey.Journey.Calls[callIndex].Start {
			continue
		}
		i++
		if i < start {
			continue
		}
		arrival, departure := estimatedCallTimes(journey.Journey, callIndex, &offset)
		if !journey.JourneyRunDate.IsZero() {
			arrival = stopPredictionTime(arrival, time.Time{}, 0, journey.JourneyRunDate, location)
			departure = stopPredictionTime(departure, time.Time{}, 0, journey.JourneyRunDate, location)
		}
		stop := journey.Stops[keys[i]]
		if stop == nil || stop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture || !stop.ArrivalTime.Equal(arrival) || !stop.DepartureTime.Equal(departure) {
			return true
		}
	}
	return false
}
