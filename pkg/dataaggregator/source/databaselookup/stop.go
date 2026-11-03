package databaselookup

import (
	"context"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (s Source) StopQuery(stopQuery query.Stop) (*ctdf.Stop, error) {
	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop

	queries := []bson.M{stopQuery.ToBson()}
	if stopQuery.Identifier != "" {
		queries = []bson.M{{"primaryidentifier": stopQuery.Identifier}, {"otheridentifiers": stopQuery.Identifier}, {"platforms.primaryidentifier": stopQuery.Identifier}}
	}
	for _, filter := range queries {
		err := stopsCollection.FindOne(context.Background(), filter).Decode(&stop)
		if err == nil {
			return stop, nil
		}
		if err != mongo.ErrNoDocuments {
			return nil, err
		}
	}
	return nil, mongo.ErrNoDocuments
}
