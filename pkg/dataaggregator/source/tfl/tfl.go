package tfl

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataaggregator/source/databaselookup"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/transforms"
	"io"
	"net/http"
	"reflect"
	"regexp"
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

		// Get all the services running at this stop locally
		//serviceNameMapping := t.getServiceNameMappings(departureBoardQuery.Stop)
		//pretty.Println(serviceNameMapping, tflOperator)

		// Query for services from the realtime_journeys table
		realtimeJourneysCollection := database.GetCollection("realtime_journeys")
		cursor, _ := realtimeJourneysCollection.Find(context.Background(), bson.M{
			fmt.Sprintf("stops.%s", tflStopID): bson.M{
				"$exists": true,
			},
		})

		var realtimeJourneys []ctdf.RealtimeJourney
		if err := cursor.All(context.Background(), &realtimeJourneys); err != nil {
			log.Error().Err(err).Msg("Failed to decode Realtime Journeys")
		}

		for _, realtimeJourney := range realtimeJourneys {
			timedOut := (time.Now().Sub(realtimeJourney.ModificationDateTime)).Minutes() > 2

			if !timedOut {
				scheduledTime := realtimeJourney.Stops[tflStopID].ArrivalTime

				// Skip over this one if we've already past its arrival time
				if scheduledTime.Before(now) {
					continue
				}

				departure := &ctdf.DepartureBoard{
					DestinationDisplay: realtimeJourney.PrimaryIdentifier,
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

func (t Source) getTflStopArrivals(stopID string) ([]tflArrivalPrediction, error) {
	url := fmt.Sprintf("https://api.tfl.gov.uk/StopPoint/%s/Arrivals?app_key=%s", stopID, t.AppKey)
	req, _ := http.NewRequest("GET", url, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"}

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		return nil, err
	}

	byteValue, _ := io.ReadAll(resp.Body)

	var arrivalPredictions []tflArrivalPrediction
	err = json.Unmarshal(byteValue, &arrivalPredictions)

	return arrivalPredictions, err
}

func (t Source) getServiceNameMappings(stop *ctdf.Stop) map[string]*ctdf.Service {
	databaseLookup := databaselookup.Source{}
	servicesQueryResult, err := databaseLookup.Lookup(query.ServicesByStop{
		Stop: stop,
	})

	serviceNameMapping := map[string]*ctdf.Service{}
	if err == nil {
		for _, service := range servicesQueryResult.([]*ctdf.Service) {
			serviceNameMapping[service.ServiceName] = service
		}
	}

	return serviceNameMapping
}

type tflArrivalPrediction struct {
	ID            string `json:"id"`
	OperationType int    `json:"operationType"`
	VehicleID     string `json:"vehicleId"`

	LineID   string `json:"lineId"`
	LineName string `json:"lineName"`

	PlatformName string `json:"platformName"`
	Direction    string `json:"direction"`

	DestinationNaptanID string `json:"destinationNaptanId"`
	DestinationName     string `json:"destinationName"`

	Towards string `json:"towards"`

	ExpectedArrival string `json:"expectedArrival"`

	ModeName string `json:"modeName"`
}

func (prediction *tflArrivalPrediction) GetDestinationDisplay(service *ctdf.Service) string {
	nameRegex := regexp.MustCompile("(.+) Underground Station")

	destinationName := prediction.DestinationName
	if destinationName == "" && prediction.Towards != "" && prediction.Towards != "Check Front of Train" {
		destinationName = prediction.Towards
	} else if destinationName == "" {
		destinationName = service.ServiceName
	}

	nameMatches := nameRegex.FindStringSubmatch(prediction.DestinationName)

	if len(nameMatches) == 2 {
		destinationName = nameMatches[1]
	}

	return destinationName
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
