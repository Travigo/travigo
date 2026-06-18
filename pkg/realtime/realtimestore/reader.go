package realtimestore

import (
	"context"
	"errors"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func FindByIdentifier(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	if err := validateIdentifier(identifier); err != nil {
		return nil, err
	}

	realtimeJourney := &ctdf.RealtimeJourney{}
	if err := collectionOrDefault(nil).FindOne(ctx, FilterByIdentifier(identifier)).Decode(realtimeJourney); err != nil {
		return nil, err
	}

	ApplyLocationDescription(ctx, realtimeJourney)
	return realtimeJourney, nil
}

func FindCurrentForJourney(ctx context.Context, journeyID string) (*ctdf.RealtimeJourney, error) {
	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()

	realtimeJourney := &ctdf.RealtimeJourney{}
	err := collectionOrDefault(nil).FindOne(ctx, bson.M{
		"journey.primaryidentifier": journeyID,
		"modificationdatetime":      bson.M{"$gt": realtimeActiveCutoffDate},
	}).Decode(realtimeJourney)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if !realtimeJourney.IsActive() {
		return nil, nil
	}

	ApplyLocationDescription(ctx, realtimeJourney)
	return realtimeJourney, nil
}

func FindActiveWithinBounds(ctx context.Context, boundsQuery bson.M) ([]*ctdf.RealtimeJourney, error) {
	realtimeActiveCutoffDate := ctdf.GetShortActiveRealtimeJourneyCutOffDate()

	cursor, err := collectionOrDefault(nil).Find(ctx,
		bson.M{
			"$and": bson.A{
				bson.M{"vehiclelocation.coordinates": boundsQuery},
				bson.M{"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate}},
			},
		},
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var realtimeJourneys []*ctdf.RealtimeJourney
	for cursor.Next(ctx) {
		realtimeJourney := &ctdf.RealtimeJourney{}
		if err := cursor.Decode(realtimeJourney); err != nil {
			return nil, err
		}

		ApplyLocationDescription(ctx, realtimeJourney)
		realtimeJourneys = append(realtimeJourneys, realtimeJourney)
	}

	return realtimeJourneys, cursor.Err()
}

func ApplyLocationDescription(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if realtimeJourney == nil {
		return
	}

	locationDescription, err := GetLocationDescription(ctx, realtimeJourney.PrimaryIdentifier)
	if err == nil {
		realtimeJourney.VehicleLocationDescription = locationDescription
	}
}

func GetLocationDescription(ctx context.Context, identifier string) (string, error) {
	description := redis_client.Client.Get(ctx, fmt.Sprintf("realtime-journey:%s/locationdescription", identifier))
	if description.Err() != nil {
		return "", description.Err()
	}
	return description.Val(), nil
}
