package databaselookup

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
)

func (s Source) RealtimeJourneyQuery(q query.RealtimeJourney) (*ctdf.RealtimeJourney, error) {
	journey, _ := realtimestore.FindOne(context.Background(), q.ToBson())

	if journey == nil {
		return nil, errors.New("could not find a matching Realtime Journey")
	} else {
		return journey, nil
	}
}
