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

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/database"
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

func (t TflSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		query := q.(query.DepartureBoard)

		// If the stop has no CrsRef then give up
		if !slices.Contains(query.Stop.TransportTypes, ctdf.TransportTypeMetro) {
			return nil, UnsupportedSourceError
		}

		tflStopID := ""

		for _, association := range query.Stop.Associations {
			if association.Type == "stop_group" {
				// TODO: USE DATA AGGREGATOR FOR THIS
				collection := database.GetCollection("stop_groups")
				var stopGroup *ctdf.StopGroup
				collection.FindOne(context.Background(), bson.M{"primaryidentifier": association.AssociatedIdentifier}).Decode(&stopGroup)

				if stopGroup.OtherIdentifiers["AtcoCode"] != "" && stopGroup.Type == "dock" {
					tflStopID = stopGroup.OtherIdentifiers["AtcoCode"]

					break
				}
			}
		}

		if tflStopID == "" {
			return nil, UnsupportedSourceError
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

		// pretty.Println(arrivalPredictions)

		var departureBoard []*ctdf.DepartureBoard
		now := time.Now()

		operatorRef := "GB:NOC:LULD"

		nameRegex := regexp.MustCompile("(.+) Underground Station")

		for _, prediction := range arrivalPredictions {
			serviceRef := fmt.Sprintf("GB:TFLSERVICE:%s", prediction.LineID)
			scheduledTime, _ := time.Parse(time.RFC3339, prediction.ExpectedArrival)

			destinationName := prediction.DestinationName
			nameMatches := nameRegex.FindStringSubmatch(prediction.DestinationName)

			if len(nameMatches) == 2 {
				destinationName = nameMatches[1]
			}

			// TODO: THIS IS JUST FOR TESTING ATM
			lineName := prediction.LineName
			if lineName == "Hammersmith & City" {
				lineName = "H&C"
			}

			departureBoard = append(departureBoard, &ctdf.DepartureBoard{
				DestinationDisplay: destinationName,
				Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
				Time:               scheduledTime.In(now.Location()),

				Journey: &ctdf.Journey{
					PrimaryIdentifier: serviceRef,

					ServiceRef: serviceRef,
					Service: &ctdf.Service{
						PrimaryIdentifier: serviceRef,
						ServiceName:       lineName,
					},

					OperatorRef: operatorRef,
					Operator: &ctdf.Operator{
						PrimaryIdentifier: operatorRef,
						PrimaryName:       "London Underground (TfL)",
					},
				},
			})
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

	ExpectedArrival string `json:"expectedArrival"`

	ModeName string `json:"modeName"`
}
