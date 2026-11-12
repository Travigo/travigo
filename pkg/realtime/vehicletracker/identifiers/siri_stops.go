package identifiers

import (
	"context"
	"errors"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Bound the work and the size of the subsequent _id filter. For larger
// endpoint sets the original cursor can stream results without an ID list.
const siriStopIDLimit = 1024

func getSiriStopJourneys(collection *mongo.Collection, date time.Time, query bson.M, exactDeparture time.Time) (siriJourneyCandidates, error) {
	origin, hasOrigin := query["path.originstopref"]
	destination, hasDestination := query["path.destinationstopref"]
	if !hasOrigin || !hasDestination {
		return getAvailableJourneys(collection, date, query, exactDeparture)
	}

	services := query["serviceref"]
	origins, covered, err := findSiriStopIDs(collection, bson.M{
		"serviceref": services, "path.originstopref": origin,
	}, database.JourneySiriOriginIDsIndex)
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
	}, database.JourneySiriDestinationIDsIndex)
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
	return getAvailableJourneys(collection, date, bson.M{
		"_id":                     bson.M{"$in": matches},
		"serviceref":              services,
		"path.originstopref":      origin,
		"path.destinationstopref": destination,
	}, exactDeparture, options.Find().SetHint("_id_"))
}

func findSiriStopIDs(collection *mongo.Collection, query bson.M, index string) ([]primitive.ObjectID, bool, error) {
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
