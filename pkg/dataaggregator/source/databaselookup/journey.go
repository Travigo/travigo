package databaselookup

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/mongo"
)

func (s Source) JourneyQuery(journeyQuery query.Journey) (*ctdf.Journey, error) {
	collection := database.GetCollection("journeys")
	var journey ctdf.Journey
	if err := collection.FindOne(context.Background(), journeyQuery.ToBson()).Decode(&journey); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("could not find a matching Journey")
		}
		return nil, err
	}
	if err := journey.ValidatePath(); err != nil {
		return nil, err
	}
	return &journey, nil
}
