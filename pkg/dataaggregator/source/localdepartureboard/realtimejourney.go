package localdepartureboard

import (
	"context"
	"encoding/json"
	"github.com/travigo/travigo/pkg/ctdf"
)

func (s Source) RealtimeJourneyForJourneyQuery(q ctdf.RealtimeJourneyForJourney) (*ctdf.RealtimeJourney, error) {
	realtimeJourneyID, err := s.realtimeJourneysStore.Get(context.Background(), q.Journey.PrimaryIdentifier)

	if err != nil {
		return nil, err
	}

	realtimeJourneyJSON, err := s.realtimeJourneysStore.Get(context.Background(), realtimeJourneyID)

	if err != nil {
		return nil, err
	}

	var realtimeJourney ctdf.RealtimeJourney
	err = json.Unmarshal([]byte(realtimeJourneyJSON), &realtimeJourney)
	if err != nil {
		return nil, err
	}

	return &realtimeJourney, nil
}
