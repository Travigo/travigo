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

type siriJourneyCandidates struct {
	available  []*ctdf.Journey
	count      int
	exactCount int
}

func getAvailableJourneys(journeysCollection *mongo.Collection, framedVehicleJourneyDate time.Time, query bson.M, exactDeparture time.Time) (siriJourneyCandidates, error) {
	var candidates siriJourneyCandidates

	// Keep precisely the fields consumed by availability, functional hashing,
	// direction, endpoint and departure-time matching.
	opts := options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "primaryidentifier", Value: 1},
		{Key: "serviceref", Value: 1},
		{Key: "direction", Value: 1},
		{Key: "departuretime", Value: 1},
		{Key: "availability", Value: 1},
		{Key: "path.originstopref", Value: 1},
		{Key: "path.destinationstopref", Value: 1},
		{Key: "path.originarrivaltime", Value: 1},
		{Key: "path.origindeparturetime", Value: 1},
		{Key: "path.destinationarrivaltime", Value: 1},
	})
	cursor, err := journeysCollection.Find(context.Background(), query, opts)
	if err != nil {
		return candidates, err
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		candidates.count++
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			log.Error().Err(err).Msg("Failed to decode journey")
			continue
		}
		if !exactDeparture.IsZero() && sameDepartureMinute(journey.DepartureTime.UTC(), exactDeparture) {
			candidates.exactCount++
		}

		// if it has no availability then we'll just ignore it
		if journey.Availability != nil && journey.Availability.MatchDate(framedVehicleJourneyDate) {
			candidates.available = append(candidates.available, &journey)
		}
	}

	return candidates, cursor.Err()
}

func sameDepartureMinute(left, right time.Time) bool {
	return left.Hour() == right.Hour() && left.Minute() == right.Minute()
}
