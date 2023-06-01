package tfl

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
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
