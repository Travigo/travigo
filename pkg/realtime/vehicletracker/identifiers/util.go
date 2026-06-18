package identifiers

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type primaryIdentifierResult struct {
	PrimaryIdentifier string `bson:"primaryidentifier"`
}

func findPrimaryIdentifiers(collection *mongo.Collection, query bson.M, limit int64) ([]string, error) {
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "primaryidentifier", Value: 1},
	}).SetLimit(limit)

	cursor, err := collection.Find(context.Background(), query, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	var identifiers []string
	for cursor.Next(context.Background()) {
		var result primaryIdentifierResult
		if err := cursor.Decode(&result); err != nil {
			return nil, err
		}

		identifiers = append(identifiers, result.PrimaryIdentifier)
	}

	return identifiers, cursor.Err()
}

func getAvailableJourneys(journeysCollection *mongo.Collection, framedVehicleJourneyDate time.Time, query bson.M) []*ctdf.Journey {
	var journeys []*ctdf.Journey

	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "otheridentifiers", Value: 0},
		bson.E{Key: "datasource", Value: 0},
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "destinationdisplay", Value: 0},
		bson.E{Key: "path.track", Value: 0},
		bson.E{Key: "path.originactivity", Value: 0},
		bson.E{Key: "path.destinationactivity", Value: 0},
	})
	cursor, err := journeysCollection.Find(context.Background(), query, opts)
	if err != nil {
		log.Error().Err(err).Msg("Failed to find available journeys")
		return journeys
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var journey *ctdf.Journey
		err := cursor.Decode(&journey)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode journey")
		}

		// if it has no availability then we'll just ignore it
		if journey.Availability != nil && journey.Availability.MatchDate(framedVehicleJourneyDate) {
			journeys = append(journeys, journey)
		}
	}

	return journeys
}
