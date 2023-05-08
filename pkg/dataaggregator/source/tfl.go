package source

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
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
}

func (t TflSource) GetName() string {
	return "Transport for London API"
}

func (t TflSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
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

func (t TflSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		departureBoardQuery := q.(query.DepartureBoard)

		tflStopID, err := getTflStopID(departureBoardQuery.Stop)

		if err != nil {
			return nil, err
		}

		source := fmt.Sprintf("https://api.tfl.gov.uk/StopPoint/%s/Arrivals", tflStopID)
		req, _ := http.NewRequest("GET", source, nil)
		req.Header["user-agent"] = []string{"curl/7.54.1"}

		client := &http.Client{}
		resp, err := client.Do(req)

		if err != nil {
			return nil, err
		}

		byteValue, _ := ioutil.ReadAll(resp.Body)

		var arrivalPredictions []tflArrivalPrediction
		json.Unmarshal(byteValue, &arrivalPredictions)

		var departureBoard []*ctdf.DepartureBoard
		now := time.Now()

		operatorRef := "GB:NOC:TFLO"

		nameRegex := regexp.MustCompile("(.+) Underground Station")

		latestDepartureTime := time.Now()

		// Get all the services running at this stop locally
		databaseLookup := DatabaseLookupSource{}
		servicesQueryResult, err := databaseLookup.Lookup(query.ServicesByStop{
			Stop: departureBoardQuery.Stop,
		})

		serviceNameMapping := map[string]*ctdf.Service{}
		if err == nil {
			for _, service := range servicesQueryResult.([]*ctdf.Service) {
				serviceNameMapping[service.ServiceName] = service
			}
		}

		// Convert the TfL results into a departure board
		for _, prediction := range arrivalPredictions {
			var service *ctdf.Service
			if serviceNameMapping[prediction.LineID] == nil {
				service = &ctdf.Service{
					PrimaryIdentifier: fmt.Sprintf("GB:TFLSERVICE:%s", prediction.LineID),
					ServiceName:       prediction.LineName,
					OperatorRef:       operatorRef,
				}
			} else {
				service = serviceNameMapping[prediction.LineID]
			}

			scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)
			scheduledTime = scheduledTime.In(now.Location())

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

			departure := &ctdf.DepartureBoard{
				DestinationDisplay: destinationName,
				Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
				Time:               scheduledTime,

				Journey: &ctdf.Journey{
					PrimaryIdentifier: "NOTIMPLEMENTEDYET",

					ServiceRef: service.PrimaryIdentifier,
					Service:    service,

					OperatorRef: operatorRef,
					Operator: &ctdf.Operator{
						PrimaryIdentifier: operatorRef,
						PrimaryName:       "Transport for London",
					},
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

type tflArrivalPrediction struct {
	ID            string `json:"id"`
	OperationType string `json:"operationType"`
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

type tflStopService struct {
	LineName string `json:"lineName"`
}
