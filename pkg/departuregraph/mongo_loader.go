package departuregraph

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoLoader struct{}

func (MongoLoader) JourneyCount(ctx context.Context) (int64, error) {
	return database.GetCollection(database.JourneysCollectionName).EstimatedDocumentCount(ctx)
}

func (MongoLoader) LoadStopJourneys(ctx context.Context, stopRefs []string, serviceDate time.Time) ([]*ctdf.Journey, error) {
	if len(stopRefs) == 0 {
		return nil, nil
	}
	cursor, err := database.GetCollection(database.JourneysCollectionName).Find(ctx, bson.M{
		"path.originstopref": bson.M{"$in": stopRefs},
	}, departureGraphFindOptions())
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	journeys := make([]*ctdf.Journey, 0, 64)
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return nil, err
		}
		if journey.Availability != nil && journey.Availability.MatchDate(serviceDate) {
			journeys = append(journeys, &journey)
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return journeys, nil
}

func (MongoLoader) ScanJourneys(ctx context.Context, visit func(*ctdf.Journey) error) error {
	cursor, err := database.GetCollection(database.JourneysCollectionName).Find(ctx, bson.M{}, departureGraphFindOptions().SetBatchSize(1000))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return fmt.Errorf("decode journey for departure graph: %w", err)
		}
		if err := visit(&journey); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func departureGraphFindOptions() *options.FindOptions {
	return options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "primaryidentifier", Value: 1},
		{Key: "otheridentifiers.BlockNumber", Value: 1},
		{Key: "datasource.datasetid", Value: 1},
		{Key: "serviceref", Value: 1},
		{Key: "operatorref", Value: 1},
		{Key: "departuretime", Value: 1},
		{Key: "departuretimezone", Value: 1},
		{Key: "destinationdisplay", Value: 1},
		{Key: "replacesjourneyrefs", Value: 1},
		{Key: "availability", Value: 1},
		{Key: "detailedrailinformation.replacementbus", Value: 1},
		{Key: "path.originstopref", Value: 1},
		{Key: "path.destinationstopref", Value: 1},
		{Key: "path.originplatform", Value: 1},
		{Key: "path.destinationplatform", Value: 1},
		{Key: "path.originarrivaltime", Value: 1},
		{Key: "path.origindeparturetime", Value: 1},
		{Key: "path.destinationarrivaltime", Value: 1},
		{Key: "path.destinationdisplay", Value: 1},
		{Key: "path.originactivity", Value: 1},
		{Key: "path.destinationactivity", Value: 1},
	})
}
