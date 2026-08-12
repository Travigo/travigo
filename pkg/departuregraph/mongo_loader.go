package departuregraph

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoLoader struct{}

func (MongoLoader) ScanStops(ctx context.Context, visit func(*ctdf.Stop) error) error {
	cursor, err := database.GetCollection("stops").Find(ctx, bson.M{}, options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "primaryidentifier", Value: 1},
		{Key: "otheridentifiers", Value: 1},
		{Key: "platforms.primaryidentifier", Value: 1},
		{Key: "location", Value: 1},
	}).SetBatchSize(2000))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return err
		}
		if err := visit(&stop); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func (MongoLoader) ScanTransfers(ctx context.Context, visit func(*ctdf.StopTransfer) error) error {
	cursor, err := database.GetCollection("stop_transfers").Find(ctx, bson.M{}, options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "fromstopref", Value: 1},
		{Key: "tostopref", Value: 1},
		{Key: "fromrouteref", Value: 1},
		{Key: "torouteref", Value: 1},
		{Key: "fromtripref", Value: 1},
		{Key: "totripref", Value: 1},
		{Key: "type", Value: 1},
		{Key: "distancemetres", Value: 1},
		{Key: "walkdurationseconds", Value: 1},
		{Key: "minchangedurationseconds", Value: 1},
		{Key: "totaldurationseconds", Value: 1},
	}).SetBatchSize(5000))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		var transfer ctdf.StopTransfer
		if err := cursor.Decode(&transfer); err != nil {
			return err
		}
		if err := visit(&transfer); err != nil {
			return err
		}
	}
	return cursor.Err()
}

type mongoJourneyRecord struct {
	ID      primitive.ObjectID `bson:"_id"`
	Journey ctdf.Journey       `bson:",inline"`
}

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

func (MongoLoader) ScanJourneys(ctx context.Context, after string, visit func(*ctdf.Journey, string) error) error {
	filter := bson.M{}
	if after != "" {
		objectID, err := primitive.ObjectIDFromHex(after)
		if err != nil {
			return fmt.Errorf("parse departure graph scan cursor: %w", err)
		}
		filter["_id"] = bson.M{"$gt": objectID}
	}
	cursor, err := database.GetCollection(database.JourneysCollectionName).Find(
		ctx,
		filter,
		departureGraphFindOptions().SetBatchSize(1000).SetSort(bson.D{{Key: "_id", Value: 1}}),
	)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		var record mongoJourneyRecord
		if err := cursor.Decode(&record); err != nil {
			return fmt.Errorf("decode journey for departure graph: %w", err)
		}
		if err := visit(&record.Journey, record.ID.Hex()); err != nil {
			return err
		}
	}
	return cursor.Err()
}

func departureGraphFindOptions() *options.FindOptions {
	return options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 1},
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
