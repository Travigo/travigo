package databaselookup

import (
	"errors"
	"reflect"

	"github.com/travigo/travigo/pkg/ctdf"
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
		reflect.TypeOf(ctdf.Operator{}),
		reflect.TypeOf(ctdf.OperatorGroup{}),
		reflect.TypeOf(ctdf.Service{}),
		reflect.TypeOf([]*ctdf.Service{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case ctdf.QueryStop:
		return s.StopQuery(q.(ctdf.QueryStop))
	case ctdf.QueryStopGroup:
		return s.StopGroupQuery(q.(ctdf.QueryStopGroup))
	case ctdf.QueryJourney:
		return s.JourneyQuery(q.(ctdf.QueryJourney))
	case ctdf.QueryOperator:
		return s.OperatorQuery(q.(ctdf.QueryOperator))
	case ctdf.QueryOperatorGroup:
		return s.OperatorGroupQuery(q.(ctdf.QueryOperatorGroup))
	case ctdf.QueryService:
		return s.ServiceQuery(q.(ctdf.QueryService))
	case ctdf.QueryServicesByStop:
		return s.ServicesByStopQuery(q.(ctdf.QueryServicesByStop))
	}

	return nil, errors.New("unable to lookup")
}
