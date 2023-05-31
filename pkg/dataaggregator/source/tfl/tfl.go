package tfl

import (
	"context"
	"fmt"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/exp/slices"
	"reflect"
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
		reflect.TypeOf(ctdf.Journey{}),
	}
}

func (t Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		return t.DepartureBoardQuery(q.(query.DepartureBoard))
	case query.Journey:
		return t.JourneyQuery(q.(query.Journey))
	}

	return nil, nil
}

func getTflStopID(stop *ctdf.Stop) (string, error) {
	// Only care about metro stops
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
