package journeyplanner

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"reflect"
)

type Source struct {
}

func (s Source) GetName() string {
	return "Journey Planner"
}

func (s Source) Supports() []reflect.Type {
	return []reflect.Type{
		reflect.TypeOf([]ctdf.JourneyPlan{}),
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
