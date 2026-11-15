package vehicletracker

import (
	"container/list"
	"context"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/sync/singleflight"
)

const stopLocationCacheCapacity = 50000

type stopLocationEntry struct {
	ref     string
	stop    *ctdf.Stop
	expires time.Time
}

var stopLocations struct {
	sync.Mutex
	entries map[string]*list.Element
	recent  list.List
	loads   singleflight.Group
}

func hydrateDestinationStops(journey *ctdf.Journey) (bool, error) {
	stops := make(map[string]*ctdf.Stop)
	var misses []string
	now := time.Now()
	stopLocations.Lock()
	for _, call := range journey.LegCalls() {
		if call.Stop != nil || call.StopRef == "" {
			continue
		}
		if _, seen := stops[call.StopRef]; seen {
			continue
		}
		stops[call.StopRef] = nil
		if element := stopLocations.entries[call.StopRef]; element != nil {
			entry := element.Value.(stopLocationEntry)
			if now.Before(entry.expires) {
				stopLocations.recent.MoveToFront(element)
				stops[call.StopRef] = entry.stop
				continue
			}
		}
		misses = append(misses, call.StopRef)
	}
	stopLocations.Unlock()

	if len(misses) > 0 {
		slices.Sort(misses)
		value, err, _ := stopLocations.loads.Do(strings.Join(misses, "\x00"), func() (any, error) {
			return loadStopLocations(misses)
		})
		if err != nil {
			return false, err
		}
		for _, ref := range misses {
			stops[ref] = value.(map[string]*ctdf.Stop)[ref]
		}
	}

	complete := true
	for _, call := range journey.LegCalls() {
		if call.Stop == nil {
			call.Stop = stops[call.StopRef]
		}
		if call.Stop == nil || call.Stop.Location == nil {
			complete = false
		}
	}
	return complete, nil
}

func loadStopLocations(refs []string) (map[string]*ctdf.Stop, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.TrackedStopQuery, time.Since(started)) }()
	ctx := context.Background()
	cursor, err := database.GetCollection("stops").Find(ctx, bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": refs}},
			bson.M{"otheridentifiers": bson.M{"$in": refs}},
		},
	}, options.Find().SetProjection(bson.M{"_id": 0, "primaryidentifier": 1, "otheridentifiers": 1, "location": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	stops := make(map[string]*ctdf.Stop)
	for cursor.Next(ctx) {
		var row ctdf.Stop
		if err := cursor.Decode(&row); err != nil {
			return nil, err
		}
		// Aliases are only needed to resolve the input references.
		stop := &ctdf.Stop{PrimaryIdentifier: row.PrimaryIdentifier, Location: row.Location}
		stops[row.PrimaryIdentifier] = stop
		for _, alias := range row.OtherIdentifiers {
			stops[alias] = stop
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	now := time.Now()
	stopLocations.Lock()
	defer stopLocations.Unlock()
	if stopLocations.entries == nil {
		stopLocations.entries = make(map[string]*list.Element)
	}
	for _, ref := range refs {
		stop := stops[ref]
		ttl := journeyTrackCacheTTL
		if stop == nil || stop.Location == nil {
			ttl = journeyTrackMissCacheTTL
		}
		if previous := stopLocations.entries[ref]; previous != nil {
			stopLocations.recent.Remove(previous)
		} else if len(stopLocations.entries) >= stopLocationCacheCapacity {
			oldest := stopLocations.recent.Back()
			delete(stopLocations.entries, oldest.Value.(stopLocationEntry).ref)
			stopLocations.recent.Remove(oldest)
		}
		stopLocations.entries[ref] = stopLocations.recent.PushFront(stopLocationEntry{ref, stop, now.Add(ttl)})
	}
	return stops, nil
}
