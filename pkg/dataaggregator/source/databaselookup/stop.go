package databaselookup

import (
	"context"
	"errors"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func (s Source) StopQuery(stopQuery query.Stop) (*ctdf.Stop, error) {
	stopsCollection := database.GetCollection("stops")
	var stop *ctdf.Stop

	if stopQuery.Identifier != "" {
		stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": stopQuery.Identifier}).Decode(&stop)
		if stop == nil {
			stopsCollection.FindOne(context.Background(), bson.M{"otheridentifiers": stopQuery.Identifier}).Decode(&stop)
		}
		if stop == nil {
			stopsCollection.FindOne(context.Background(), bson.M{"platforms.primaryidentifier": stopQuery.Identifier}).Decode(&stop)
		}
	} else {
		stopsCollection.FindOne(context.Background(), stopQuery.ToBson()).Decode(&stop)
	}

	if stop == nil {
		return nil, errors.New("could not find a matching Stop")
	} else {
		return stop, nil
	}
}
