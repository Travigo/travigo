package identifiers

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
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

// Cache metadata and raw calls so inactive candidates and exact matches
// never need their full call sequence decoded.
type siriStopCandidate struct {
	PrimaryIdentifier  string
	ServiceRef         string
	Direction          string
	DepartureTime      time.Time
	DestinationDisplay string
	Availability       *ctdf.Availability
	Calls              bson.RawValue

	pathOnce sync.Once
	path     []ctdf.JourneyCall
	pathErr  error
}

type siriCandidateRows struct {
	rows              []*siriStopCandidate
	count, bytes      int
	missingCallArrays bool
}

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
		return entry.value.forStopEvent(key, date, exactDeparture, direction)
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
		} else if !rows.missingCallArrays && rows.count == len(rows.rows) && rows.bytes <= siriStopCandidateCacheMaxBytes {
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
	return value.(siriCandidateRows).forStopEvent(key, date, exactDeparture, direction)
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
	count, err := scanSiriCandidateDocuments(collection, fetch, telemetry.SiriCandidateDocuments, func(cursor *mongo.Cursor) error {
		row := new(siriStopCandidate)
		if err := cursor.Decode(row); err != nil {
			log.Error().Err(err).Msg("Failed to decode SIRI stop candidate")
			return nil
		}
		rows.missingCallArrays = rows.missingCallArrays || row.Calls.Type != bson.TypeArray
		rows.rows = append(rows.rows, row)
		rows.bytes += len(cursor.Current)
		return nil
	}, options.Find().SetHint("_id_"), siriCandidateProjection)
	rows.count = count
	return rows, err
}

func (rows siriCandidateRows) forStopEvent(key string, date, aimed time.Time, direction string) (siriJourneyCandidates, error) {
	candidates := siriJourneyCandidates{count: rows.count}
	var activeRows []int
	for index, row := range rows.rows {
		if !aimed.IsZero() && sameDepartureMinute(row.DepartureTime.UTC(), aimed) {
			candidates.exactCount++
		}
		if row.Availability == nil || !row.Availability.MatchDate(date) {
			continue
		}
		activeRows = append(activeRows, index)
		candidates.available = append(candidates.available, &ctdf.Journey{
			PrimaryIdentifier: row.PrimaryIdentifier, ServiceRef: row.ServiceRef, Direction: row.Direction,
			DepartureTime: row.DepartureTime, DestinationDisplay: row.DestinationDisplay, Availability: row.Availability,
		})
	}

	if !aimed.IsZero() && rows.count == len(rows.rows) {
		candidates.exactMatch = rows.exactStopDeparture(candidates, aimed, direction)
		if candidates.exactMatch != nil {
			return candidates, nil
		}
	}
	for active, index := range activeRows {
		row := rows.rows[index]
		journey := candidates.available[active]
		row.pathOnce.Do(func() {
			if row.Calls.Type != 0 {
				row.pathErr = row.Calls.Unmarshal(&row.path)
				row.Calls = bson.RawValue{}
			}
			if row.pathErr == nil {
				path := ctdf.Journey{PrimaryIdentifier: row.PrimaryIdentifier, Calls: row.path}
				row.pathErr = path.ValidatePath()
			}
		})
		if row.pathErr != nil {
			siriStopCandidates.remove(key)
			return candidates, fmt.Errorf("decode SIRI journey path %q: %w", journey.PrimaryIdentifier, row.pathErr)
		}
		journey.Calls = row.path
	}
	return candidates, nil
}

func (rows siriCandidateRows) exactStopDeparture(candidates siriJourneyCandidates, aimed time.Time, direction string) *ctdf.Journey {
	if direction != "1" && direction != "2" {
		return exactSiriDepartureMatch(candidates, aimed, direction)
	}
	count := 0
	for _, journey := range rows.rows {
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
