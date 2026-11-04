package nyct

import (
	"context"
	"errors"
	"regexp"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// IdentifyJourney resolves a subway trip against the active static schedule,
// preferring its full path and rejecting ambiguous shortened matches.
func IdentifyJourney(tripID, startDate string) (string, error) {
	tripID = TripID(tripID)
	key := TripKey(tripID)
	date, err := time.Parse("20060102", startDate)
	if key == "" || err != nil {
		return "", errors.New("Invalid NYC subway trip ID or service date")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{
		"datasource.datasetid":         SubwayDataset,
		"otheridentifiers.GTFS-TripID": bson.M{"$regex": "^" + regexp.QuoteMeta(key) + "[A-Za-z0-9]*$"},
	}, options.Find().SetProjection(bson.M{"primaryidentifier": 1, "availability": 1, "otheridentifiers.GTFS-TripID": 1}))
	if err != nil {
		return "", err
	}
	defer cursor.Close(ctx)
	var matched, exact string
	matches, exactMatches := 0, 0
	// Do not limit before calendar filtering: earlier candidates may be inactive.
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return "", err
		}
		if journey.Availability != nil && journey.Availability.MatchDate(date) {
			matches++
			matched = journey.PrimaryIdentifier
			if journey.OtherIdentifiers["GTFS-TripID"] == tripID {
				exactMatches++
				exact = journey.PrimaryIdentifier
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return "", err
	}
	// A supplied path disambiguates same-time trains. Without one, all active
	// candidates must agree on a single journey; never guess between paths.
	if tripID != key && exactMatches == 1 {
		return exact, nil
	}
	if exactMatches > 1 || matches > 1 {
		return "", errors.New("Ambiguous NYC subway trip")
	}
	if matches == 0 {
		return "", errors.New("Could not find referenced trip")
	}
	return matched, nil
}
