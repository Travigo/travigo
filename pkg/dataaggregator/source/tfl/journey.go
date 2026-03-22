package tfl

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/redis_client"
)

func (s Source) JourneyQuery(journeyQuery query.Journey) (*ctdf.Journey, error) {
	tflJourneyRegex, _ := regexp.Compile("realtime-tfl-.*")

	if !tflJourneyRegex.MatchString(journeyQuery.PrimaryIdentifier) {
		return nil, source.UnsupportedSourceError
	}

	var realtimeJourney ctdf.RealtimeJourney
	realtimeJourneyJSON := redis_client.Client.Get(context.Background(), journeyQuery.PrimaryIdentifier)
	if realtimeJourneyJSON.Val() == "" {
		return nil, errors.New("failed to find requested TfL journey")
	}

	err := json.Unmarshal([]byte(realtimeJourneyJSON.Val()), &realtimeJourney)
	if err != nil {
		return nil, errors.New("failed to unmarshal requested TfL journey")
	}

	return realtimeJourney.Journey, nil
}
