package nationalrail

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"reflect"
)

type Source struct {
	GatewayEndpoint string
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
	case ctdf.QueryDepartureBoard:
		return s.DepartureBoardQuery(q.(ctdf.QueryDepartureBoard))
	default:
		return nil, source.UnsupportedSourceError
	}
}
