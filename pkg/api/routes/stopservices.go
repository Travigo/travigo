package routes

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/stopservices"
)

// lookupStopServicesOrEmpty keeps stop responses available before the initial
// materialization is published. Other lookup failures still propagate.
func lookupStopServicesOrEmpty(ctx context.Context, stops []*ctdf.Stop) (map[string][]*ctdf.Service, error) {
	servicesByStop, err := dataaggregator.Lookup[map[string][]*ctdf.Service](query.ServicesByStops{
		Stops:   stops,
		Context: ctx,
	})
	if !errors.Is(err, stopservices.ErrUnbuilt) {
		return servicesByStop, err
	}

	servicesByStop = make(map[string][]*ctdf.Service, len(stops))
	for _, stop := range stops {
		if stop != nil && stop.PrimaryIdentifier != "" {
			servicesByStop[stop.PrimaryIdentifier] = []*ctdf.Service{}
		}
	}
	return servicesByStop, nil
}
