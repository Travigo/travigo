package tfl

import (
	"context"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/transforms"
)

var (
	backfillSource     localdepartureboard.Source
	backfillSourceOnce sync.Once
)

func getBackfillSource() *localdepartureboard.Source {
	backfillSourceOnce.Do(func() {
		backfillSource.Setup()
	})
	return &backfillSource
}

func (s Source) DepartureBoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	q.Type = ctdf.BoardTypeDeparture
	return s.BoardQuery(q)
}

// BoardQuery uses TfL's per-stop predicted-arrival feed for both board modes.
// TfL does not provide a separate predicted-departure timestamp in this feed;
// retaining this behaviour keeps departure responses compatible while allowing
// arrivals to use the same realtime data and scheduled backfill.
func (s Source) BoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "gb-noc-TFLO",
		PrimaryName:       "Transport for London",
	}

	now := time.Now()

	isTFLStop := false
	var services []*ctdf.Service
	services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
		Stop: q.Stop,
	})

	for _, service := range services {
		if service.OperatorRef == tflOperator.PrimaryIdentifier {
			isTFLStop = true
			break
		}
	}

	log.Debug().Str("Length", time.Now().Sub(now).String()).Msg("Check if TfL service")

	if !isTFLStop {
		return nil, source.UnsupportedSourceError
	}

	var departureBoard []*ctdf.DepartureBoard

	now = time.Now()
	latestDepartureTime := now

	stopTimezone, err := time.LoadLocation(q.Stop.Timezone)
	if err != nil || stopTimezone == nil {
		stopTimezone = time.UTC
	}

	allStopIDS := append(q.Stop.OtherIdentifiers, q.Stop.PrimaryIdentifier)
	realtimeJourneys, err := realtimestore.FindTFLDepartureBoardJourneys(context.Background(), allStopIDS, now.Add(-30*time.Second))
	if err != nil {
		log.Error().Err(err).Msg("Failed to query TfL realtime journeys")
	}

	log.Debug().Str("Length", time.Now().Sub(now).String()).Msg("Query TfL realtime journeys")

	generateDeparteBoardStart := time.Now()

	for _, realtimeJourney := range realtimeJourneys {
		timedOut := (now.Sub(realtimeJourney.ModificationDateTime)).Minutes() > 2

		if !timedOut {
			realtimeJourneyStop := realtimeJourney.Stops[q.Stop.PrimaryIdentifier]
			if realtimeJourneyStop == nil {
				for _, stopID := range allStopIDS {
					if realtimeJourney.Stops[stopID] != nil {
						realtimeJourneyStop = realtimeJourney.Stops[stopID]
						break
					}
				}
			}
			if realtimeJourneyStop == nil || realtimeJourneyStop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
				continue
			}

			scheduledTime := realtimeJourneyStop.ArrivalTime.In(stopTimezone)

			// Skip over this one if we've already past its arrival time (allow 30 second overlap)
			if scheduledTime.Before(now.Add(-30 * time.Second)) {
				continue
			}

			departure := &ctdf.DepartureBoard{
				DestinationDisplay: realtimeJourney.Journey.DestinationDisplay,
				Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
				Time:               scheduledTime,

				Journey: realtimeJourney.Journey,
			}
			realtimeJourney.Journey.GetService()
			departure.Journey.Operator = tflOperator
			departure.Journey.OperatorRef = tflOperator.PrimaryIdentifier

			platform := realtimeJourneyStop.Platform

			if platform != "" {
				departure.Platform = platform
				departure.PlatformType = "ACTUAL"
			}

			transforms.Transform(departure.Journey.Service, 2)
			transforms.Transform(departure.Journey.Operator, 2)
			departureBoard = append(departureBoard, departure)

			if scheduledTime.After(latestDepartureTime) {
				latestDepartureTime = scheduledTime
			}
		}
	}

	log.Debug().Str("Length", time.Now().Sub(generateDeparteBoardStart).String()).Msg("Generate TfL departure board from realtime journeys")

	// If the realtime data doesnt provide enough to cover our request then fill in with the local departure board
	remainingCount := q.Count - len(departureBoard)

	if remainingCount > 0 {
		localSource := getBackfillSource()

		q.StartDateTime = latestDepartureTime
		q.Count = remainingCount

		localDepartures, err := localSource.Lookup(q)

		if err == nil {
			departureBoard = append(departureBoard, localDepartures.([]*ctdf.DepartureBoard)...)
		}
	}

	return departureBoard, nil
}
