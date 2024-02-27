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
	cursor, _ := journeysCollection.Find(context.Background(), query, opts)

	for cursor.Next(context.TODO()) {
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
