package tfl

import (
	"context"
	"errors"
	"regexp"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func (s Source) JourneyQuery(journeyQuery query.Journey) (*ctdf.Journey, error) {
	tflJourneyRegex, _ := regexp.Compile("realtime-tfl-.*")

	if !tflJourneyRegex.MatchString(journeyQuery.PrimaryIdentifier) {
		return nil, source.UnsupportedSourceError
	}

	collection := database.GetCollection("realtime_journeys")
	var realtimeJourney *ctdf.RealtimeJourney
	collection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyQuery.PrimaryIdentifier}).Decode(&realtimeJourney)

	if realtimeJourney == nil {
		return nil, errors.New("failed to find requested TfL journey")
	}

	return realtimeJourney.Journey, nil
}
