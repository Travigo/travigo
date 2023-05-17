package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"regexp"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
)

type TflSource struct {
	AppKey string
}

func (t TflSource) GetName() string {
	return "Transport for London API"
}

func (t TflSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
}

func (t TflSource) Lookup(q any) (interface{}, error) {
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

		latestDepartureTime := time.Now()

		// Get all the services running at this stop locally
		serviceNameMapping := t.getServiceNameMappings(departureBoardQuery.Stop)

		arrivalPredictions, err := t.getTflStopArrivals(tflStopID)
		if err != nil {
			return nil, err
		}

		// Convert the TfL results into a departure board
		for _, prediction := range arrivalPredictions {
			var service *ctdf.Service
			if serviceNameMapping[prediction.LineName] == nil {
				service = &ctdf.Service{
					PrimaryIdentifier: fmt.Sprintf("GB:TFLSERVICE:%s", prediction.LineID),
					ServiceName:       prediction.LineName,
					OperatorRef:       tflOperator.PrimaryIdentifier,
				}
			} else {
				service = serviceNameMapping[prediction.LineName]
			}

			scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)
			scheduledTime = scheduledTime.In(now.Location())

			departure := &ctdf.DepartureBoard{
				DestinationDisplay: prediction.GetDestinationDisplay(service),
				Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
				Time:               scheduledTime,

				Journey: &ctdf.Journey{
					PrimaryIdentifier: "",

					ServiceRef: service.PrimaryIdentifier,
					Service:    service,

					OperatorRef: tflOperator.PrimaryIdentifier,
					Operator:    tflOperator,
				},
			}
			transforms.Transform(departure, 2)
			departureBoard = append(departureBoard, departure)

			if scheduledTime.After(latestDepartureTime) {
				latestDepartureTime = scheduledTime
			}
		}

		// If the realtime data doesnt provide enough to cover our request then fill in with the local departure board
		remainingCount := departureBoardQuery.Count - len(departureBoard)

		if remainingCount > 0 {
			localSource := LocalDepartureBoardSource{}

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

func (t TflSource) getTflStopArrivals(stopID string) ([]tflArrivalPrediction, error) {
	source := fmt.Sprintf("https://api.tfl.gov.uk/StopPoint/%s/Arrivals?app_key=%s", stopID, t.AppKey)
	req, _ := http.NewRequest("GET", source, nil)
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

func (t TflSource) getServiceNameMappings(stop *ctdf.Stop) map[string]*ctdf.Service {
	databaseLookup := DatabaseLookupSource{}
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
		return "", UnsupportedSourceError
	}

	tflStopID := ""

	for _, association := range stop.Associations {
		if association.Type == "stop_group" {
			// TODO: USE DATA AGGREGATOR FOR THIS
			collection := database.GetCollection("stop_groups")
			var stopGroup *ctdf.StopGroup
			collection.FindOne(context.Background(), bson.M{"primaryidentifier": association.AssociatedIdentifier}).Decode(&stopGroup)

			if stopGroup.OtherIdentifiers["AtcoCode"] != "" && stopGroup.Type == "station" {
				tflStopID = stopGroup.OtherIdentifiers["AtcoCode"]

				break
			}
		}
	}

	if tflStopID == "" {
		return tflStopID, UnsupportedSourceError
	}

	return tflStopID, nil
}
