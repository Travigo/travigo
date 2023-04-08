package source

import (
	"context"
	"errors"
	"reflect"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/dataaggregator/query"
	"github.com/britbus/britbus/pkg/database"
)

type DatabaseLookupSource struct {
}

func (d DatabaseLookupSource) GetName() string {
	return "Database Lookup"
}

func (d DatabaseLookupSource) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(ctdf.Stop{}),
		reflect.TypeOf(ctdf.StopGroup{}),
		reflect.TypeOf(ctdf.Journey{}),
		reflect.TypeOf(ctdf.RealtimeJourney{}),
		reflect.TypeOf(ctdf.Operator{}),
		reflect.TypeOf(ctdf.OperatorGroup{}),
		reflect.TypeOf(ctdf.Service{}),
	}
}

func (d DatabaseLookupSource) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.Stop:
		stopQuery := q.(query.Stop)

		stopsCollection := database.GetCollection("stops")
		var stop *ctdf.Stop
		stopsCollection.FindOne(context.Background(), stopQuery.ToBson()).Decode(&stop)

		if stop == nil {
			return nil, errors.New("Could not find a matching Stop")
		} else {
			return stop, nil
		}
	case query.StopGroup:
		stopQuery := q.(query.StopGroup)

		collection := database.GetCollection("stop_groups")
		var stopGroup *ctdf.StopGroup
		collection.FindOne(context.Background(), stopQuery.ToBson()).Decode(&stopGroup)

		if stopGroup == nil {
			return nil, errors.New("Could not find a matching Stop Group")
		} else {
			return stopGroup, nil
		}
	case query.Journey:
		query := q.(query.Journey)

		collection := database.GetCollection("journeys")
		var journey *ctdf.Journey
		collection.FindOne(context.Background(), query.ToBson()).Decode(&journey)

		if journey == nil {
			return nil, errors.New("Could not find a matching Journey")
		} else {
			return journey, nil
		}
	case query.Operator:
		query := q.(query.Operator)

		collection := database.GetCollection("operators")
		var operator *ctdf.Operator
		collection.FindOne(context.Background(), query.ToBson()).Decode(&operator)

		if operator == nil {
			return nil, errors.New("Could not find a matching Operator")
		} else {
			return operator, nil
		}
	case query.OperatorGroup:
		query := q.(query.OperatorGroup)

		collection := database.GetCollection("operator_groups")
		var operator *ctdf.OperatorGroup
		collection.FindOne(context.Background(), query.ToBson()).Decode(&operator)

		if operator == nil {
			return nil, errors.New("Could not find a matching Operator Group")
		} else {
			return operator, nil
		}
	case query.Service:
		query := q.(query.Service)

		collection := database.GetCollection("services")
		var service *ctdf.Service
		collection.FindOne(context.Background(), query.ToBson()).Decode(&service)

		if service == nil {
			return nil, errors.New("Could not find a matching Service")
		} else {
			return service, nil
		}
	case query.RealtimeJourney:
		query := q.(query.RealtimeJourney)

		collection := database.GetCollection("realtime_journeys")
		var journey *ctdf.RealtimeJourney
		collection.FindOne(context.Background(), query.ToBson()).Decode(&journey)

		if journey == nil {
			return nil, errors.New("Could not find a matching Realtime Journey")
		} else {
			return journey, nil
		}
	}

	return nil, errors.New("Unable to lookup")
}
