package realtimestore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	mongooptions "go.mongodb.org/mongo-driver/mongo/options"
)

func FindByIdentifier(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	if err := validateIdentifier(identifier); err != nil {
		return nil, err
	}

	if realtimeJourney, err := GetRealtimeJourney(ctx, identifier); err == nil {
		ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
		return realtimeJourney, nil
	}

	realtimeJourney := &ctdf.RealtimeJourney{}
	if err := collectionOrDefault(nil).FindOne(ctx, FilterByIdentifier(identifier)).Decode(realtimeJourney); err != nil {
		return nil, err
	}

	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
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

	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)

	if !realtimeJourney.IsActive() {
		return nil, nil
	}

	return realtimeJourney, nil
}

func FindCurrentForJourneyIDs(ctx context.Context, journeyIDs []string) (map[string]*ctdf.RealtimeJourney, error) {
	realtimeJourneysByJourneyID := map[string]*ctdf.RealtimeJourney{}
	if len(journeyIDs) == 0 {
		return realtimeJourneysByJourneyID, nil
	}

	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()
	realtimeJourneyProjection := bson.D{
		bson.E{Key: "primaryidentifier", Value: 1},
		bson.E{Key: "activelytracked", Value: 1},
		bson.E{Key: "modificationdatetime", Value: 1},
		bson.E{Key: "timeoutdurationminutes", Value: 1},
		bson.E{Key: "stops", Value: 1},
		bson.E{Key: "cancelled", Value: 1},
		bson.E{Key: "vehiclelocation", Value: 1},
		bson.E{Key: "journey.primaryidentifier", Value: 1},
		bson.E{Key: "journey.path.destinationstopref", Value: 1},
		bson.E{Key: "journey.path.destinationarrivaltime", Value: 1},
	}

	cursor, err := collectionOrDefault(nil).Find(ctx, bson.M{
		"journey.primaryidentifier": bson.M{"$in": journeyIDs},
		"modificationdatetime":      bson.M{"$gt": realtimeActiveCutoffDate},
	}, mongooptions.Find().SetProjection(realtimeJourneyProjection))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	for cursor.Next(ctx) {
		realtimeJourney := &ctdf.RealtimeJourney{}
		if err := cursor.Decode(realtimeJourney); err != nil {
			return nil, err
		}

		if realtimeJourney.Journey != nil {
			ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
			realtimeJourneysByJourneyID[realtimeJourney.Journey.PrimaryIdentifier] = realtimeJourney
		}
	}

	return realtimeJourneysByJourneyID, cursor.Err()
}

func FindCurrentByJourneyRefs(ctx context.Context, journeyRefs []string) (*ctdf.RealtimeJourney, error) {
	if len(journeyRefs) == 0 {
		return nil, nil
	}

	realtimeActiveCutoffDate := ctdf.GetActiveRealtimeJourneyCutOffDate()

	realtimeJourney := &ctdf.RealtimeJourney{}
	err := collectionOrDefault(nil).FindOne(ctx, bson.M{
		"journeyref":           bson.M{"$in": journeyRefs},
		"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate},
	}).Decode(realtimeJourney)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
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

		ApplyRealtimeJourneyOverlays(ctx, realtimeJourney)
		realtimeJourneys = append(realtimeJourneys, realtimeJourney)
	}

	return realtimeJourneys, cursor.Err()
}

func ApplyRealtimeJourneyOverlays(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	ApplyLocation(ctx, realtimeJourney)
	ApplyLocationDescription(ctx, realtimeJourney)
}

func ApplyLocation(ctx context.Context, realtimeJourney *ctdf.RealtimeJourney) {
	if realtimeJourney == nil {
		return
	}

	location, bearing, err := GetLocation(ctx, realtimeJourney.PrimaryIdentifier)
	if err == nil {
		realtimeJourney.VehicleLocation = location
		realtimeJourney.VehicleBearing = bearing
	}
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

func GetLocation(ctx context.Context, identifier string) (ctdf.Location, float64, error) {
	locationResult := redis_client.Client.Get(ctx, fmt.Sprintf("realtime-journey:%s/location", identifier))
	if locationResult.Err() != nil {
		return ctdf.Location{}, 0, locationResult.Err()
	}

	var vehicleLocation storedVehicleLocation
	if err := json.Unmarshal([]byte(locationResult.Val()), &vehicleLocation); err != nil {
		return ctdf.Location{}, 0, err
	}

	return vehicleLocation.Location, vehicleLocation.Bearing, nil
}

func GetRealtimeJourney(ctx context.Context, identifier string) (*ctdf.RealtimeJourney, error) {
	realtimeJourneyResult := redis_client.Client.Get(ctx, realtimeJourneyDetailsKey(identifier))
	if realtimeJourneyResult.Err() != nil {
		return nil, realtimeJourneyResult.Err()
	}

	realtimeJourney := &ctdf.RealtimeJourney{}
	if err := json.Unmarshal([]byte(realtimeJourneyResult.Val()), realtimeJourney); err != nil {
		return nil, err
	}

	return realtimeJourney, nil
}
