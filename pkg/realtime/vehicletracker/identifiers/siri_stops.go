package identifiers

import (
	"context"
	"errors"
	"fmt"
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

// Only retain small projected candidate sets. Dates, direction and aimed
// departure are applied after lookup so different observations can share them.
var siriStopCandidates = lookupCache[siriCandidateRows]{limit: 128, ttl: 3 * time.Minute, missTTL: 30 * time.Second}

const siriStopCandidateCacheMaxBytes = 512 << 10

var errSiriStopScanRequired = errors.New("SIRI stop lookup requires a direct query")

func getSiriStopJourneys(collection *mongo.Collection, date time.Time, query bson.M, exactDeparture time.Time, direction string) (siriJourneyCandidates, error) {
	origin, hasOrigin := query["originstoprefs"]
	destination, hasDestination := query["destinationstoprefs"]
	if !hasOrigin || !hasDestination {
		return getAvailableJourneys(collection, date, query, exactDeparture)
	}
	// Callers supply a scalar service or a single $in clause with sorted IDs.
	keyFields, err := bson.Marshal(bson.D{{Key: "service", Value: query["serviceref"]}, {Key: "origin", Value: origin}, {Key: "destination", Value: destination}})
	if err != nil {
		return siriJourneyCandidates{}, err
	}
	key := collection.Database().Name() + "\x00" + collection.Name() + "\x00" + string(keyFields)
	if entry, ok := siriStopCandidates.lookup(key); ok {
		telemetry.Add(telemetry.SiriStopCandidateCacheHit, 1)
		return entry.value.forStopEvent(date, exactDeparture, direction)
	}
	value, err, _ := siriStopCandidates.loads.Do(key, func() (any, error) {
		if entry, ok := siriStopCandidates.lookup(key); ok {
			telemetry.Add(telemetry.SiriStopCandidateCacheHit, 1)
			return entry.value, nil
		}

		rows, err := loadSiriStopRows(collection, query["serviceref"], origin, destination)
		if err != nil {
			return nil, err
		}
		if rows.count == 0 {
			siriStopCandidates.store(key, rows, errSiriJourney)
		} else if rows.pathErr == nil && rows.count == len(rows.journeys) && rows.bytes <= siriStopCandidateCacheMaxBytes {
			siriStopCandidates.store(key, rows, nil)
		} else if rows.bytes > siriStopCandidateCacheMaxBytes {
			telemetry.Add(telemetry.SiriStopCandidateCacheOversize, 1)
		}
		return rows, nil
	})
	if errors.Is(err, errSiriStopScanRequired) {
		return getAvailableJourneys(collection, date, query, exactDeparture)
	}
	if err != nil {
		return siriJourneyCandidates{}, err
	}
	return value.(siriCandidateRows).forStopEvent(date, exactDeparture, direction)
}

func loadSiriStopRows(collection *mongo.Collection, services, origin, destination any) (siriCandidateRows, error) {
	origins, covered, err := findSiriStopIDs(collection, bson.M{
		"serviceref": services, "originstoprefs": origin,
	}, database.JourneyOriginStopServiceIDsIndex)
	if err != nil {
		return siriCandidateRows{}, err
	}
	if !covered {
		return siriCandidateRows{}, errSiriStopScanRequired
	}
	if len(origins) == 0 {
		return siriCandidateRows{}, nil
	}

	destinations, covered, err := findSiriStopIDs(collection, bson.M{
		"serviceref": services, "destinationstoprefs": destination,
	}, database.JourneyDestinationStopServiceIDsIndex)
	if err != nil {
		return siriCandidateRows{}, err
	}
	if !covered {
		return siriCandidateRows{}, errSiriStopScanRequired
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
		return siriCandidateRows{}, nil
	}

	// Recheck both stops in case a timetable import updated a journey between
	// reads. The _id hint keeps this fetch limited to the intersected documents.
	fetch := bson.M{
		"_id":                 bson.M{"$in": matches},
		"serviceref":          services,
		"originstoprefs":      origin,
		"destinationstoprefs": destination,
	}
	var rows siriCandidateRows
	count, err := scanSiriCandidates(collection, fetch, telemetry.SiriCandidateDocuments, false, func(journey *ctdf.Journey, bytes int) {
		if rows.pathErr == nil {
			rows.pathErr = journey.ValidatePath()
			if rows.pathErr != nil {
				rows.pathErr = fmt.Errorf("decode SIRI journey path %q: %w", journey.PrimaryIdentifier, rows.pathErr)
			}
		}
		rows.journeys = append(rows.journeys, journey)
		rows.bytes += bytes
	}, options.Find().SetHint("_id_"), siriCandidateProjection)
	rows.count = count
	return rows, err
}

func (rows siriCandidateRows) forStopEvent(date, aimed time.Time, direction string) (siriJourneyCandidates, error) {
	candidates := rows.forEvent(date, aimed)
	if !aimed.IsZero() && rows.count == len(rows.journeys) {
		candidates.exactMatch = rows.exactStopDeparture(candidates, aimed, direction)
	}
	// Exact departure only requires metadata. An invalid path still blocks
	// the broader fallback, where it could otherwise hide a competing match.
	if candidates.exactMatch != nil {
		return candidates, nil
	}
	return candidates, rows.pathErr
}

func (rows siriCandidateRows) exactStopDeparture(candidates siriJourneyCandidates, aimed time.Time, direction string) *ctdf.Journey {
	if direction != "1" && direction != "2" {
		return exactSiriDepartureMatch(candidates, aimed, direction)
	}
	count := 0
	for _, journey := range rows.journeys {
		if journey.Direction == direction || sameDepartureMinute(journey.DepartureTime.UTC(), aimed) {
			count++
		}
	}
	if count >= siriExactCandidateLimit {
		return nil
	}
	match := exactSiriDepartureMatch(candidates, aimed, direction)
	// Some feeds send a numeric direction while the timetable stores another
	// convention. Only ignore it when no preferred-direction journey runs.
	if match == nil && !slices.ContainsFunc(candidates.available, func(journey *ctdf.Journey) bool {
		return journey.Direction == direction
	}) {
		match = exactSiriDepartureMatch(candidates, aimed, "")
	}
	return match
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
