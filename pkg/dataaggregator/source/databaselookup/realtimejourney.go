package databaselookup

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) RealtimeJourneyQuery(q ctdf.QueryRealtimeJourney) (*ctdf.RealtimeJourney, error) {
	collection := database.GetCollection("realtime_journeys")
	var journey *ctdf.RealtimeJourney
	collection.FindOne(context.Background(), q.ToBson()).Decode(&journey)

	if journey == nil {
		return nil, errors.New("could not find a matching Realtime Journey")
	} else {
		return journey, nil
	}
}
