package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/transforms"
)

func (s Source) DepartureBoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
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

	log.Debug().Str("Length", time.Since(now).String()).Msg("Check if TfL service")

	if !isTFLStop {
		return nil, source.UnsupportedSourceError
	}

	var departureBoard []*ctdf.DepartureBoard

	now = time.Now()
	latestDepartureTime := now

	stopTimezone, _ := time.LoadLocation(q.Stop.Timezone)

	var realtimeJourneys []ctdf.RealtimeJourney

	allStopIDS := append(q.Stop.OtherIdentifiers, q.Stop.PrimaryIdentifier)

	for _, stopID := range allStopIDS {
		keysIter := redis_client.Client.Scan(context.Background(), 0, fmt.Sprintf("tfl-realtime-stop-mapping-%s-*", stopID), 0).Iterator()
		for keysIter.Next(context.Background()) {
			mappingKey := keysIter.Val()
			realtimeJourneyID := redis_client.Client.Get(context.Background(), mappingKey)
			realtimeJourneyJSON := redis_client.Client.Get(context.Background(), realtimeJourneyID.Val())
			if realtimeJourneyJSON.Val() != "" {
				var realtimeJourney ctdf.RealtimeJourney
				err := json.Unmarshal([]byte(realtimeJourneyJSON.Val()), &realtimeJourney)
				if err != nil {
					log.Error().Err(err).Msg("Error unmarshalling realtime journey")
					continue
				}
				realtimeJourneys = append(realtimeJourneys, realtimeJourney)
			}
		}
	}

	log.Debug().Str("Length", time.Since(now).String()).Msg("Query TfL realtime journeys")

	generateDeparteBoardStart := time.Now()

	for _, realtimeJourney := range realtimeJourneys {
		timedOut := (time.Since(realtimeJourney.ModificationDateTime)).Minutes() > 2

		if !timedOut {
			if realtimeJourney.Stops[q.Stop.PrimaryIdentifier] == nil {
				continue
			}

			scheduledTime := realtimeJourney.Stops[q.Stop.PrimaryIdentifier].ArrivalTime.In(stopTimezone)

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

			platform := realtimeJourney.Stops[q.Stop.PrimaryIdentifier].Platform

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

	log.Debug().Str("Length", time.Since(generateDeparteBoardStart).String()).Msg("Generate TfL departure board from realtime journeys")

	// If the realtime data doesnt provide enough to cover our request then fill in with the local departure board
	remainingCount := q.Count - len(departureBoard)

	if remainingCount > 0 {
		localSource := localdepartureboard.Source{}
		localSource.Setup() //TODO maybe not

		q.StartDateTime = latestDepartureTime
		q.Count = remainingCount

		localDepartures, err := localSource.Lookup(q)

		if err == nil {
			departureBoard = append(departureBoard, localDepartures.([]*ctdf.DepartureBoard)...)
		}
	}

	return departureBoard, nil
}
