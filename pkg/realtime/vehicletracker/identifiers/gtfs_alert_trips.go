package identifiers

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var alertTrips = lookupCache[string]{limit: 16384, ttl: 5 * time.Minute, missTTL: 30 * time.Second}

type alertTrip struct {
	dataset, id, date, clock string
}

func alertTripFromReference(ref map[string]string) alertTrip {
	trip := alertTrip{dataset: ref["LinkedDataset"], id: ref["TripID"], clock: ref["TripStartTime"]}
	if trip.dataset == nyct.SubwayDataset {
		trip.date = ref["TripStartDate"]
		trip.clock = ""
	}
	return trip
}

func (t alertTrip) key() string {
	return lookupKey(t.dataset, t.id, t.date, t.clock)
}

// ResolveAlertTrips returns one result per reference. Stop and route references
// are still handled independently, even when they share a trip descriptor.
func ResolveAlertTrips(refs []map[string]string) ([]string, error) {
	resolved := make(map[alertTrip]string)
	pending := make(map[string][]alertTrip)
	seen := make(map[alertTrip]struct{})
	for _, ref := range refs {
		trip := alertTripFromReference(ref)
		if trip.id == "" || trip.dataset == "" {
			continue
		}
		if _, ok := seen[trip]; ok {
			continue
		}
		seen[trip] = struct{}{}
		if cached, ok := alertTrips.lookup(trip.key()); ok {
			telemetry.Add(telemetry.AlertJourneyCacheHits, 1)
			resolved[trip] = cached.value
		} else {
			pending[trip.dataset] = append(pending[trip.dataset], trip)
		}
	}

	var lookupErr error
	for dataset, trips := range pending {
		for chunk := range slices.Chunk(trips, 128) {
			if err := resolveAlertTripChunk(dataset, chunk, resolved); err != nil && lookupErr == nil {
				lookupErr = err
			}
		}
	}
	results := make([]string, len(refs))
	for i, ref := range refs {
		results[i] = resolved[alertTripFromReference(ref)]
	}
	return results, lookupErr
}

func resolveAlertTripChunk(dataset string, trips []alertTrip, resolved map[alertTrip]string) error {
	if dataset != nyct.SubwayDataset {
		const maxRows = 512
		ids := make([]string, 0, len(trips))
		for _, trip := range trips {
			ids = append(ids, trip.id)
		}
		slices.Sort(ids)
		ids = slices.Compact(ids)
		rows, err := findAlertTripCandidates(dataset, ids, maxRows+1)
		if err != nil {
			return err
		}
		if len(rows) <= maxRows {
			type match struct {
				id    string
				count int
			}
			all := make(map[string]match, len(ids))
			exact := make(map[[2]string]match)
			for _, row := range rows {
				id := row.OtherIdentifiers["GTFS-TripID"]
				clock := row.OtherIdentifiers["GTFS-TripStartTime"]
				all[id] = match{row.PrimaryIdentifier, all[id].count + 1}
				if clock != "" {
					key := [2]string{id, clock}
					exact[key] = match{row.PrimaryIdentifier, exact[key].count + 1}
				}
			}
			for _, trip := range trips {
				result := exact[[2]string{trip.id, trip.clock}]
				if result.count == 0 {
					result = all[trip.id]
				}
				if result.count == 1 {
					resolved[trip] = result.id
					alertTrips.store(trip.key(), result.id, nil)
				} else {
					alertTrips.store(trip.key(), "", errGTFSJourneyNotFound)
				}
			}
			return nil
		}
		if len(trips) > 1 {
			// Keep other trips batched if a frequency-expanded trip exceeds the
			// row cap. Only a single oversized lookup needs the legacy query.
			mid := len(trips) / 2
			return errors.Join(
				resolveAlertTripChunk(dataset, trips[:mid], resolved),
				resolveAlertTripChunk(dataset, trips[mid:], resolved),
			)
		}
	}

	// NYC subway has service-date and path matching rules. An oversized
	// individual lookup also needs the legacy queries, which limit each read.
	var lookupErr error
	for _, trip := range trips {
		matcher := GTFSRT{IdentifyingInformation: map[string]string{
			"LinkedDataset": trip.dataset, "TripID": trip.id, "TripStartDate": trip.date, "TripStartTime": trip.clock,
		}}
		id, err := matcher.identifyJourney(telemetry.AlertJourneyQuery)
		if err == nil || errors.Is(err, errGTFSJourneyNotFound) || errors.Is(err, nyct.ErrTripNotFound) ||
			errors.Is(err, nyct.ErrInvalidTrip) || errors.Is(err, nyct.ErrAmbiguousTrip) {
			resolved[trip] = id
			alertTrips.store(trip.key(), id, err)
		} else if lookupErr == nil {
			lookupErr = err
		}
	}
	return lookupErr
}

type alertTripCandidate struct {
	PrimaryIdentifier string            `bson:"primaryidentifier"`
	OtherIdentifiers  map[string]string `bson:"otheridentifiers"`
}

func findAlertTripCandidates(dataset string, ids []string, limit int64) ([]alertTripCandidate, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.AlertJourneyQuery, time.Since(started)) }()
	ctx := context.Background()
	cursor, err := database.GetCollection("journeys").Find(ctx, bson.M{
		"datasource.datasetid":         datasetIDFilter(dataset),
		"otheridentifiers.GTFS-TripID": bson.M{"$in": ids},
	}, options.Find().SetLimit(limit).SetProjection(bson.M{
		"_id": 0, "primaryidentifier": 1,
		"otheridentifiers.GTFS-TripID": 1, "otheridentifiers.GTFS-TripStartTime": 1,
	}))
	if err != nil {
		return nil, err
	}
	var rows []alertTripCandidate
	err = cursor.All(ctx, &rows)
	return rows, err
}
