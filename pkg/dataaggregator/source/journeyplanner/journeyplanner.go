package journeyplanner

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/departuregraph"
	"github.com/travigo/travigo/pkg/util"
	"reflect"
)

type Source struct {
	JourneyGraph *departuregraph.Client
}

func (s *Source) Setup() {
	if address := util.GetEnvironmentVariables()["TRAVIGO_DEPARTURE_GRAPH_ADDRESS"]; address != "" {
		s.JourneyGraph = departuregraph.NewClient(address, nil)
	}
}

func (s Source) GetName() string {
	return "Journey Planner"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf(ctdf.JourneyPlanResults{}),
	}
}

func (s Source) Lookup(q any) (interface{}, error) {
	switch q.(type) {
	case query.JourneyPlan:
		return s.JourneyPlanQuery(q.(query.JourneyPlan))
	default:
		return nil, source.UnsupportedSourceError
	}
}
