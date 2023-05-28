package databaselookup

import (
	"context"
	"errors"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
)

func (s Source) JourneyQuery(journeyQuery query.Journey) (*ctdf.Journey, error) {
	collection := database.GetCollection("journeys")
	var journey *ctdf.Journey
	collection.FindOne(context.Background(), journeyQuery.ToBson()).Decode(&journey)

	if journey == nil {
		return nil, errors.New("could not find a matching Journey")
	} else {
		return journey, nil
	}
}
