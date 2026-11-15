package identifiers

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
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
	exactMatch *ctdf.Journey
}

func getAvailableJourneys(journeysCollection *mongo.Collection, framedVehicleJourneyDate time.Time, query bson.M, exactDeparture time.Time, findOpts ...*options.FindOptions) (siriJourneyCandidates, error) {

	return readSiriCandidates(journeysCollection, framedVehicleJourneyDate, query, exactDeparture, telemetry.SiriCandidateDocuments, true, append(findOpts, siriCandidateProjection)...)
}

var siriCandidateProjection = options.Find().SetProjection(bson.D{

	{Key: "_id", Value: 0},
	{Key: "primaryidentifier", Value: 1},
	{Key: "serviceref", Value: 1},
	{Key: "direction", Value: 1},
	{Key: "departuretime", Value: 1},
	{Key: "destinationdisplay", Value: 1},
	{Key: "availability", Value: 1},
	{Key: "calls.stopref", Value: 1},
	{Key: "calls.start", Value: 1},
	{Key: "calls.end", Value: 1},
	{Key: "calls.arrival", Value: 1},
	{Key: "calls.departure", Value: 1},
})

type siriCandidateRows struct {
	journeys     []*ctdf.Journey
	count, bytes int
	pathErr      error
}

func readSiriCandidates(collection *mongo.Collection, date time.Time, query bson.M, aimed time.Time, documents telemetry.Counter, includePath bool, opts ...*options.FindOptions) (siriJourneyCandidates, error) {
	var candidates siriJourneyCandidates
	count, err := scanSiriCandidates(collection, query, documents, includePath, func(journey *ctdf.Journey, _ int) {
		candidates.add(journey, date, aimed)
	}, opts...)
	candidates.count = count
	return candidates, err
}

func scanSiriCandidates(collection *mongo.Collection, query bson.M, documents telemetry.Counter, includePath bool, add func(*ctdf.Journey, int), opts ...*options.FindOptions) (int, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.SiriCandidateQuery, time.Since(started)) }()
	count := 0
	defer func() { telemetry.Add(documents, int64(count)) }()
	cursor, err := collection.Find(context.Background(), query, opts...)
	if err != nil {
		return 0, err
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		count++
		var decoded ctdf.Journey
		if err := cursor.Decode(&decoded); err != nil {
			log.Error().Err(err).Msg("Failed to decode journey")
			continue
		}
		if includePath {
			if err := decoded.ValidatePath(); err != nil {
				return count, fmt.Errorf("decode SIRI journey path %q: %w", decoded.PrimaryIdentifier, err)
			}
		}
		add(&decoded, len(cursor.Current))
	}
	return count, cursor.Err()
}

func (rows siriCandidateRows) forEvent(date, exactDeparture time.Time) siriJourneyCandidates {
	candidates := siriJourneyCandidates{count: rows.count}
	for _, journey := range rows.journeys {
		candidates.add(journey, date, exactDeparture)
	}
	return candidates
}

func (candidates *siriJourneyCandidates) add(journey *ctdf.Journey, date, exactDeparture time.Time) {
	if !exactDeparture.IsZero() && sameDepartureMinute(journey.DepartureTime.UTC(), exactDeparture) {
		candidates.exactCount++
	}
	if journey.Availability != nil && journey.Availability.MatchDate(date) {
		candidates.available = append(candidates.available, journey)
	}
}

func sameDepartureMinute(left, right time.Time) bool {
	return left.Hour() == right.Hour() && left.Minute() == right.Minute()
}
