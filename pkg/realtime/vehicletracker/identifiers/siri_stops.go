package identifiers

import (
	"context"
	"errors"
	"maps"
	"slices"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Bound the work and the size of the subsequent _id filter. For larger
// endpoint sets the original cursor can stream results without an ID list.
const siriStopIDLimit = 1024

func getSiriStopJourneys(collection *mongo.Collection, date time.Time, query bson.M, exactDeparture time.Time, direction string) (siriJourneyCandidates, error) {
	origin, hasOrigin := query["path.originstopref"]
	destination, hasDestination := query["path.destinationstopref"]
	if !hasOrigin || !hasDestination {
		return getAvailableJourneys(collection, date, query, exactDeparture)
	}

	services := query["serviceref"]
	origins, covered, err := findSiriStopIDs(collection, bson.M{
		"serviceref": services, "path.originstopref": origin,
	}, database.JourneyOriginStopServiceIDsIndex)
	if err != nil {
		return siriJourneyCandidates{}, err
	}
	if !covered {
		return getAvailableJourneys(collection, date, query, exactDeparture)
	}
	if len(origins) == 0 {
		return siriJourneyCandidates{}, nil
	}

	destinations, covered, err := findSiriStopIDs(collection, bson.M{
		"serviceref": services, "path.destinationstopref": destination,
	}, database.JourneyDestinationStopServiceIDsIndex)
	if err != nil {
		return siriJourneyCandidates{}, err
	}
	if !covered {
		return getAvailableJourneys(collection, date, query, exactDeparture)
	}

	// The stops can be on different legs of the same journey.
	destinationIDs := make(map[primitive.ObjectID]struct{}, len(destinations))
	for _, id := range destinations {
		destinationIDs[id] = struct{}{}
	}
	matches := origins[:0]
	for _, id := range origins {
		if _, ok := destinationIDs[id]; ok {
			matches = append(matches, id)
		}
	}
	if len(matches) == 0 {
		return siriJourneyCandidates{}, nil
	}

	// Recheck both stops in case a timetable import updated a journey between
	// reads. The _id hint keeps this fetch limited to the intersected documents.
	fetch := bson.M{
		"_id":                     bson.M{"$in": matches},
		"serviceref":              services,
		"path.originstopref":      origin,
		"path.destinationstopref": destination,
	}
	if !exactDeparture.IsZero() {
		probe := maps.Clone(fetch)
		// Timetable BSON dates can encode overnight times on a later day.
		// Compare the UTC clock, as the existing candidate selector does.
		exactClock := bson.M{"$cond": bson.A{
			bson.M{"$eq": bson.A{bson.M{"$type": "$departuretime"}, "date"}},
			bson.M{"$and": bson.A{
				bson.M{"$eq": bson.A{bson.M{"$hour": "$departuretime"}, exactDeparture.Hour()}},
				bson.M{"$eq": bson.A{bson.M{"$minute": "$departuretime"}, exactDeparture.Minute()}},
			}},
			false,
		}}
		probe["$expr"] = exactClock
		numericDirection := direction == "1" || direction == "2"
		if numericDirection {
			// Include off-time journeys in the preferred direction. The full
			// matcher only ignores direction if none are active that day.
			probe["$expr"] = bson.M{"$or": bson.A{
				exactClock, bson.M{"$eq": bson.A{"$direction", direction}},
			}}
		}
		candidates, err := readSiriCandidates(collection, date, probe, exactDeparture, telemetry.SiriExactProbeDocuments,
			options.Find().SetHint("_id_").SetLimit(siriExactCandidateLimit).SetProjection(bson.M{
				"_id": 0, "primaryidentifier": 1, "departuretime": 1, "direction": 1, "availability": 1,
			}))
		if err == nil && candidates.count < siriExactCandidateLimit {
			candidates.exactMatch = exactSiriDepartureMatch(candidates, exactDeparture, direction)
			if candidates.exactMatch == nil && numericDirection {
				hasPreferredDirection := slices.ContainsFunc(candidates.available, func(journey *ctdf.Journey) bool {
					return journey.Direction == direction
				})
				if !hasPreferredDirection {
					candidates.exactMatch = exactSiriDepartureMatch(candidates, exactDeparture, "")
				}
			}
			if candidates.exactMatch != nil {
				telemetry.Add(telemetry.SiriExactMatches, 1)
				return candidates, nil
			}
		}
	}
	return getAvailableJourneys(collection, date, fetch, exactDeparture, options.Find().SetHint("_id_"))
}

func findSiriStopIDs(collection *mongo.Collection, query bson.M, index string) ([]primitive.ObjectID, bool, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.SiriStopIndexQuery, time.Since(started)) }()
	ctx := context.Background()
	opts := options.Find().SetHint(index).SetProjection(bson.D{{Key: "_id", Value: 1}}).SetLimit(siriStopIDLimit + 1)
	cursor, err := collection.Find(ctx, query, opts)
	if err != nil {
		var serverErr mongo.ServerError
		if errors.As(err, &serverErr) && serverErr.HasErrorCodeWithMessage(2, "hint provided does not correspond to an existing index") {
			// The tracker can roll out before the timetable importer builds the indexes.
			return nil, false, nil
		}
		return nil, false, err
	}
	defer cursor.Close(ctx)

	var ids []primitive.ObjectID
	for cursor.Next(ctx) {
		id, ok := cursor.Current.Lookup("_id").ObjectIDOK()
		if !ok || len(ids) == siriStopIDLimit {
			return nil, false, nil
		}
		ids = append(ids, id)
	}
	return ids, true, cursor.Err()
}
