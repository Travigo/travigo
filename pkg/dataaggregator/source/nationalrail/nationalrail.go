package nationalrail

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"reflect"
)

type Source struct {
}

func (s Source) GetName() string {
	return "GB National Rail"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		return s.DepartureBoardQuery(q.(query.DepartureBoard))
	default:
		return nil, source.UnsupportedSourceError
	}
}
