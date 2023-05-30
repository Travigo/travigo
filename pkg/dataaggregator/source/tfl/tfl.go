package tfl

import (
	"context"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/transforms"
	"reflect"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
)

type Source struct {
	AppKey string
}

func (t Source) GetName() string {
	return "Transport for London API"
}

func (t Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
}

func (t Source) Lookup(q any) (interface{}, error) {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "GB:NOC:TFLO",
		PrimaryName:       "Transport for London",
	}

	switch q.(type) {
	case query.DepartureBoard:
		departureBoardQuery := q.(query.DepartureBoard)

		tflStopID, err := getTflStopID(departureBoardQuery.Stop)

		if err != nil {
			return nil, err
		}

		var departureBoard []*ctdf.DepartureBoard
		now := time.Now()

		latestDepartureTime := now

		// Query for services from the realtime_journeys table
		realtimeJourneysCollection := database.GetCollection("realtime_journeys")
		cursor, _ := realtimeJourneysCollection.Find(context.Background(), bson.M{
			fmt.Sprintf("stops.%s.timetype", tflStopID): ctdf.RealtimeJourneyStopTimeEstimatedFuture,
		})

		var realtimeJourneys []ctdf.RealtimeJourney
		if err := cursor.All(context.Background(), &realtimeJourneys); err != nil {
			log.Error().Err(err).Msg("Failed to decode Realtime Journeys")
		}

		for _, realtimeJourney := range realtimeJourneys {
			timedOut := (time.Now().Sub(realtimeJourney.ModificationDateTime)).Minutes() > 2

			if !timedOut {
				scheduledTime := realtimeJourney.Stops[tflStopID].ArrivalTime

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

				transforms.Transform(departure, 2)
				departureBoard = append(departureBoard, departure)

				if scheduledTime.After(latestDepartureTime) {
					latestDepartureTime = scheduledTime
				}
			}
		}

		// If the realtime data doesnt provide enough to cover our request then fill in with the local departure board
		remainingCount := departureBoardQuery.Count - len(departureBoard)

		if remainingCount > 0 {
			localSource := localdepartureboard.Source{}

			departureBoardQuery.StartDateTime = latestDepartureTime
			departureBoardQuery.Count = remainingCount

			localDepartures, err := localSource.Lookup(departureBoardQuery)

			if err == nil {
				departureBoard = append(departureBoard, localDepartures.([]*ctdf.DepartureBoard)...)
			}
		}

		return departureBoard, nil
	}

	return nil, nil
}

func getTflStopID(stop *ctdf.Stop) (string, error) {
	// If the stop has no CrsRef then give up
	if !slices.Contains(stop.TransportTypes, ctdf.TransportTypeMetro) {
		return "", source.UnsupportedSourceError
	}

	tflStopID := ""

	for _, association := range stop.Associations {
		if association.Type == "stop_group" {
			// TODO: USE DATA AGGREGATOR FOR THIS
			collection := database.GetCollection("stop_groups")
			var stopGroup *ctdf.StopGroup
			collection.FindOne(context.Background(), bson.M{"primaryidentifier": association.AssociatedIdentifier}).Decode(&stopGroup)

			if stopGroup != nil && stopGroup.OtherIdentifiers["AtcoCode"] != "" && stopGroup.Type == "station" {
				tflStopID = stopGroup.OtherIdentifiers["AtcoCode"]

				break
			}
		}
	}

	if tflStopID == "" {
		return tflStopID, source.UnsupportedSourceError
	}

	return fmt.Sprintf("GB:TFL:STOP:%s", tflStopID), nil
}
