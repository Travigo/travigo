package databaselookup

import (
	"context"
	"errors"
	"strings"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
)

func (s Source) RealtimeJourneyQuery(q query.RealtimeJourney) (*ctdf.RealtimeJourney, error) {
	var realtimeJourney *ctdf.RealtimeJourney
	if !strings.HasPrefix(q.PrimaryIdentifier, "gb-nationalrailrealtime-") {
		redisRealtimeJourney, err := realtimestore.GetRealtimeJourney(context.Background(), q.PrimaryIdentifier)
		if err == nil && redisRealtimeJourney.Journey != nil {
			return redisRealtimeJourney, nil
		}
	}

	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeJourneysCollection.FindOne(context.Background(), q.ToBson()).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		return nil, errors.New("failed to find requested realtime journey")
	}

	realtimestore.SetRealtimeJourney(context.Background(), realtimeJourney)

	return realtimeJourney, nil
}
