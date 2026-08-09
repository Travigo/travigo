package localdepartureboard

import (
	"reflect"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/departuregraph"
	"github.com/travigo/travigo/pkg/util"
)

type Source struct {
	CachedResults  *cachedresults.Cache
	DepartureGraph departuregraph.Provider
}

func (s *Source) Setup() {
	s.CachedResults = &cachedresults.Cache{}
	s.CachedResults.Setup()

	if address := util.GetEnvironmentVariables()["TRAVIGO_DEPARTURE_GRAPH_ADDRESS"]; address != "" {
		s.DepartureGraph = departuregraph.NewClient(address, nil)
	}
}

func (s Source) GetName() string {
	return "Local Departure Board Generator"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]*ctdf.DepartureBoard{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.DepartureBoard:
		return s.BoardQuery(q.(query.DepartureBoard))
	default:
		return nil, source.UnsupportedSourceError
	}
}
