package databaselookup

import (
	"errors"
	"reflect"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

type Source struct {
}

func (s Source) GetName() string {
	return "Database Lookup"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(ctdf.Stop{}),
		reflect.TypeOf(ctdf.StopGroup{}),
		reflect.TypeOf(ctdf.Journey{}),
		reflect.TypeOf(ctdf.RealtimeJourney{}),
		reflect.TypeOf(ctdf.Operator{}),
		reflect.TypeOf(ctdf.OperatorGroup{}),
		reflect.TypeOf(ctdf.Service{}),
		reflect.TypeOf([]*ctdf.Service{}),
		reflect.TypeOf([]*ctdf.ServiceAlert{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.Stop:
		return s.StopQuery(q.(query.Stop))
	case query.StopGroup:
		return s.StopGroupQuery(q.(query.StopGroup))
	case query.Journey:
		return s.JourneyQuery(q.(query.Journey))
	case query.Operator:
		return s.OperatorQuery(q.(query.Operator))
	case query.OperatorGroup:
		return s.OperatorGroupQuery(q.(query.OperatorGroup))
	case query.Service:
		return s.ServiceQuery(q.(query.Service))
	case query.ServicesByStop:
		return s.ServicesByStopQuery(q.(query.ServicesByStop))
	case query.RealtimeJourney:
		return s.RealtimeJourneyQuery(q.(query.RealtimeJourney))
	case query.ServiceAlertsForMatchingIdentifier:
		return s.ServiceAlertsForMatchingIdentifierQuery(q.(query.ServiceAlertsForMatchingIdentifier))
	case query.ServiceAlertsForMatchingIdentifiers:
		return s.ServiceAlertsForMatchingIdentifiersQuery(q.(query.ServiceAlertsForMatchingIdentifiers))
	}

	return nil, errors.New("unable to lookup")
}
