package vehicletracker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
)

func (consumer *BatchConsumer) updateRealtimeJourney(journeyID string, vehicleUpdateEvent *VehicleUpdateEvent) error {
	currentTime := vehicleUpdateEvent.RecordedAt

	realtimeJourneyIdentifier := fmt.Sprintf(ctdf.RealtimeJourneyIDFormat, vehicleUpdateEvent.VehicleLocationUpdate.Timeframe, journeyID)

	realtimeJourney, _ := realtimestore.FindForUpdate(context.Background(), realtimeJourneyIdentifier)
	var realtimeJourneyReliability ctdf.RealtimeJourneyReliabilityType

	newRealtimeJourney := false
	if realtimeJourney != nil && realtimeJourney.Journey == nil {
		log.Error().Msg("RealtimeJourney without a Journey found, deleting")
		// realtimeJourneysCollection.DeleteOne(context.Background(), searchQuery) TODO
		return errors.New("RealtimeJourney without a Journey found, deleting")
	}

	cachedJourney, err := consumer.getCachedTrackedJourney(journeyID, currentTime)
	if err != nil {
		return err
	}

	if realtimeJourney == nil {
		journeyDate, _ := time.Parse("2006-01-02", vehicleUpdateEvent.VehicleLocationUpdate.Timeframe)

		realtimeJourney = &ctdf.RealtimeJourney{
			PrimaryIdentifier:      realtimeJourneyIdentifier,
			ActivelyTracked:        true,
			TimeoutDurationMinutes: 10,
			Journey:                cachedJourney.Journey,
			JourneyRunDate:         journeyDate,
			Service:                cachedJourney.Journey.Service,

			CreationDateTime: currentTime,

			VehicleRef: vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier,
			Stops:      map[string]*ctdf.RealtimeJourneyStops{},
		}
		newRealtimeJourney = true
	} else {
		realtimeJourney.Journey = cachedJourney.Journey
		realtimeJourney.Service = cachedJourney.Journey.Service
	}

	var offset time.Duration
	journeyStopUpdates := map[string]*ctdf.RealtimeJourneyStops{}
	var closestDistanceJourneyPath *ctdf.JourneyPathItem // TODO maybe not here?

	// Calculate everything based on location if we aren't provided with updates
	if len(vehicleUpdateEvent.VehicleLocationUpdate.StopUpdates) == 0 && vehicleUpdateEvent.VehicleLocationUpdate.Location.Type == "Point" {
		position, matched := matchJourneyPosition(realtimeJourney.Journey, vehicleUpdateEvent.VehicleLocationUpdate.Location)
		if !matched || position.PathIndex < 0 || position.PathIndex >= len(realtimeJourney.Journey.Path) {
			return errors.New("unable to match vehicle location to journey track")
		}
		closestDistanceJourneyPathIndex := position.PathIndex
		closestDistanceJourneyPath = realtimeJourney.Journey.Path[position.PathIndex]
		closestDistanceJourneyPathPercentComplete := position.LegProgress
		if position.UsedGlobalTrack {
			realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithoutTrack
		} else {
			realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityLocationWithTrack
		}

		// Calculate new stop arrival times
		realtimeTimeframe, err := time.Parse("2006-01-02", vehicleUpdateEvent.VehicleLocationUpdate.Timeframe)
		if err != nil {
			log.Error().Err(err).Msg("Failed to parse realtime time frame")
		}

		if closestDistanceJourneyPath == nil {
			return errors.New("nil closestdistancejourneypath")
		}

		journeyTimezone := consumer.loadLocation(realtimeJourney.Journey.DepartureTimezone)

		// Get the arrival & departure times with date of the journey
		destinationArrivalTimeWithDate := serviceTimeOnDate(realtimeTimeframe, closestDistanceJourneyPath.DestinationArrivalTime, journeyTimezone)
		originDepartureTimeWithDate := serviceTimeOnDate(realtimeTimeframe, closestDistanceJourneyPath.OriginDepartureTime, journeyTimezone)

		// How long it take to travel between origin & destination
		currentPathTraversalTime := destinationArrivalTimeWithDate.Sub(originDepartureTimeWithDate)

		// How far we are between origin & departure (% of journey path, NOT time or metres)
		// TODO: this is a hack, replace with actual distance
		currentPathPercentageComplete := closestDistanceJourneyPathPercentComplete

		// Calculate what the expected time of the current position of the vehicle should be
		currentPathPositionExpectedTime := originDepartureTimeWithDate.Add(
			time.Duration(int(currentPathPercentageComplete * float64(currentPathTraversalTime.Nanoseconds()))))

		// Offset is how far behind or ahead the vehicle is from its positions expected time
		offset = currentTime.Sub(currentPathPositionExpectedTime).Round(10 * time.Second)

		// If the offset is too small then just turn it to zero so we can mark buses as on time
		if absDuration(offset) <= 45*time.Second {
			offset = time.Duration(0)
		}

		// Calculate all the estimated stop arrival & departure times
		for i := closestDistanceJourneyPathIndex; i < len(realtimeJourney.Journey.Path); i++ {
			// Don't update the database if theres no actual change
			if (offset.Seconds() == realtimeJourney.Offset.Seconds()) && !newRealtimeJourney {
				break
			}

			path := realtimeJourney.Journey.Path[i]

			arrivalTime := path.DestinationArrivalTime.Add(offset).Round(time.Minute)
			var departureTime time.Time

			if i < len(realtimeJourney.Journey.Path)-1 {
				nextPath := realtimeJourney.Journey.Path[i+1]

				if arrivalTime.Before(nextPath.OriginDepartureTime) {
					departureTime = nextPath.OriginDepartureTime
				} else {
					departureTime = arrivalTime
				}
			}

			journeyStopUpdates[path.DestinationStopRef] = &ctdf.RealtimeJourneyStops{
				StopRef:  path.DestinationStopRef,
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,

				ArrivalTime:   arrivalTime,
				DepartureTime: departureTime,
			}
		}
	} else {
		for _, stopUpdate := range vehicleUpdateEvent.VehicleLocationUpdate.StopUpdates {
			arrivalTime := stopUpdate.ArrivalTime
			departureTime := stopUpdate.DepartureTime

			if arrivalTime.Year() == 1970 {
				path := cachedJourney.PathByOriginStopRef[stopUpdate.StopID]
				if path != nil {
					arrivalTime = path.OriginArrivalTime.Add(time.Duration(stopUpdate.ArrivalOffset) * time.Second)
				}
			}
			if departureTime.Year() == 1970 {
				path := cachedJourney.PathByOriginStopRef[stopUpdate.StopID]
				if path != nil {
					departureTime = path.OriginDepartureTime.Add(time.Duration(stopUpdate.DepartureOffset) * time.Second)
				}
			}

			journeyStopUpdates[stopUpdate.StopID] = &ctdf.RealtimeJourneyStops{
				StopRef:  stopUpdate.StopID,
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,

				ArrivalTime:   arrivalTime,
				DepartureTime: departureTime,
			}
		}

		closestPathTime := 9999999 * time.Minute
		now := time.Now()
		realtimeTimeframe, err := time.Parse("2006-01-02", vehicleUpdateEvent.VehicleLocationUpdate.Timeframe)

		journeyTimezone := consumer.loadLocation(realtimeJourney.Journey.DepartureTimezone)

		if err != nil {
			log.Error().Err(err).Msg("Failed to parse realtime time frame")
		}
		for _, path := range realtimeJourney.Journey.Path {
			refTime := time.Date(
				realtimeTimeframe.Year(),
				realtimeTimeframe.Month(),
				realtimeTimeframe.Day(),
				path.OriginArrivalTime.Hour(),
				path.OriginArrivalTime.Minute(),
				path.OriginArrivalTime.Second(),
				path.OriginArrivalTime.Nanosecond(),
				journeyTimezone,
			)

			if journeyStopUpdates[path.OriginStopRef] != nil {
				refTime = journeyStopUpdates[path.OriginStopRef].ArrivalTime
			}

			if refTime.Before(now) && now.Sub(refTime) < closestPathTime {
				closestDistanceJourneyPath = path

				closestPathTime = now.Sub(refTime)
			}
		}

		realtimeJourneyReliability = ctdf.RealtimeJourneyReliabilityExternalProvided
	}

	if closestDistanceJourneyPath == nil {
		return errors.New("unable to find next journeypath")
	}

	if vehicleUpdateEvent.VehicleLocationUpdate.Location.Type != "" {
		_ = realtimestore.UpdateLocationForRealtimeJourney(
			context.Background(),
			realtimeJourney,
			vehicleUpdateEvent.VehicleLocationUpdate.Location,
			vehicleUpdateEvent.VehicleLocationUpdate.Bearing,
		)
	}

	// Update database
	realtimeJourney.ModificationDateTime = currentTime
	realtimeJourney.DepartedStopRef = closestDistanceJourneyPath.OriginStopRef
	realtimeJourney.NextStopRef = closestDistanceJourneyPath.DestinationStopRef
	realtimeJourney.Occupancy = vehicleUpdateEvent.VehicleLocationUpdate.Occupancy
	realtimeJourney.Reliability = realtimeJourneyReliability
	realtimeJourney.VehicleRef = vehicleUpdateEvent.VehicleLocationUpdate.VehicleIdentifier
	realtimeJourney.DataSource = vehicleUpdateEvent.DataSource
	// "vehiclelocationdescription": fmt.Sprintf("Passed %s", closestDistanceJourneyPath.OriginStop.PrimaryName),

	if (offset.Seconds() != realtimeJourney.Offset.Seconds()) || newRealtimeJourney {
		realtimeJourney.Offset = offset
	}

	if realtimeJourney.NextStopRef != closestDistanceJourneyPath.DestinationStopRef {
		journeyStopUpdates[realtimeJourney.NextStopRef] = &ctdf.RealtimeJourneyStops{
			StopRef:  realtimeJourney.NextStopRef,
			TimeType: ctdf.RealtimeJourneyStopTimeHistorical,

			// TODO this should obviously be a different time
			ArrivalTime:   currentTime,
			DepartureTime: currentTime,
		}
	}

	for key, stopUpdate := range journeyStopUpdates {
		if key != "" {
			if realtimeJourney.Stops[key] == nil {
				realtimeJourney.Stops[key] = &ctdf.RealtimeJourneyStops{}
			}
			realtimeJourney.Stops[key] = stopUpdate
		}
	}

	realtimestore.SaveRealtimeJourney(context.Background(), realtimeJourney)

	return nil
}

func serviceTimeOnDate(serviceDate time.Time, serviceTime time.Time, location *time.Location) time.Time {
	if location == nil {
		location = time.Local
	}
	serviceDayStart := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, location)
	encodedStart := time.Date(0, time.January, 1, 0, 0, 0, 0, serviceTime.Location())
	return serviceDayStart.Add(serviceTime.Sub(encodedStart))
}

func absDuration(value time.Duration) time.Duration {
	if value < 0 {
		return -value
	}
	return value
}
