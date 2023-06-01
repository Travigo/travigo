package tfl

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"time"
)

func (s Source) DepartureBoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "GB:NOC:TFLO",
		PrimaryName:       "Transport for London",
	}

	var departureBoard []*ctdf.DepartureBoard
	now := time.Now()

	latestDepartureTime := now

	// Query for services from the realtime_journeys table
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	cursor, _ := realtimeJourneysCollection.Find(context.Background(), bson.M{
		fmt.Sprintf("stops.%s.timetype", q.Stop.PrimaryIdentifier): ctdf.RealtimeJourneyStopTimeEstimatedFuture,
	})

	var realtimeJourneys []ctdf.RealtimeJourney
	if err := cursor.All(context.Background(), &realtimeJourneys); err != nil {
		log.Error().Err(err).Msg("Failed to decode Realtime Journeys")
	}

	log.Debug().Str("Length", time.Now().Sub(now).String()).Msg("Query TfL realtime journeys")

	generateDeparteBoardStart := time.Now()

	for _, realtimeJourney := range realtimeJourneys {
		timedOut := (time.Now().Sub(realtimeJourney.ModificationDateTime)).Minutes() > 2

		if !timedOut {
			scheduledTime := realtimeJourney.Stops[q.Stop.PrimaryIdentifier].ArrivalTime

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
		localSource := localdepartureboard.Source{}

		q.StartDateTime = latestDepartureTime
		q.Count = remainingCount

		localDepartures, err := localSource.Lookup(q)

		if err == nil {
			departureBoard = append(departureBoard, localDepartures.([]*ctdf.DepartureBoard)...)
		}
	}

	return departureBoard, nil
}
