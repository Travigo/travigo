package vehicletracker

import (
	"context"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

// Performance Review [Set J] [PR148] [P2] (tracker): This limit is per consumer: five independent
// caches per process can retain the same journeys, stops, services, and geometry,
// with no affinity routing to keep updates on a warm cache. Share immutable static
// records within a process, coalesce concurrent misses, and cache reusable tracks
// by TrackRef. Bound retained bytes as well as entry count, and invalidate/version
// timetable data; keep mutable realtime state outside the shared cache.
const trackedJourneyCacheMaxEntries = 1000

type cachedTrackedJourney struct {
	Journey         *ctdf.Journey
	StopOccurrences map[string][]int
	LastUsed        time.Time
}

func (consumer *BatchConsumer) getCachedTrackedJourney(journeyID string, now time.Time) (*cachedTrackedJourney, error) {
	consumer.journeyCacheMu.RLock()
	cachedJourney := consumer.journeyCache[journeyID]
	consumer.journeyCacheMu.RUnlock()

	if cachedJourney != nil {
		consumer.journeyCacheMu.Lock()
		cachedJourney.LastUsed = now
		consumer.journeyCacheMu.Unlock()

		return cachedJourney, nil
	}

	journey, err := loadTrackedJourney(journeyID)
	if err != nil {
		return nil, err
	}

	// Performance Review [Set J] [PR149] [P2] (tracker): Every cold journey hydrates full destination
	// stop documents, tracks, and service data, including GTFS updates containing only
	// stop times and no position. Split lightweight schedule/occurrence metadata from
	// geometry, loading geometry only when matching actually needs it. For matching,
	// project stop identifiers/aliases and location rather than retaining whole stop
	// documents. Keep separately enriched API/event views compatible with consumers.
	if err := hydrateDestinationStops(journey); err != nil {
		log.Warn().Err(err).Str("journey", journeyID).Msg("Failed to hydrate journey destination stops")
	}
	journey.GetTracks()

	journey.GetService()

	cachedJourney = &cachedTrackedJourney{
		Journey:         journey,
		StopOccurrences: journeyStopOccurrences(journey),
		LastUsed:        now,
	}

	consumer.journeyCacheMu.Lock()
	if consumer.journeyCache == nil {
		consumer.journeyCache = map[string]*cachedTrackedJourney{}
	}
	consumer.journeyCache[journeyID] = cachedJourney
	consumer.pruneJourneyCacheLocked()
	consumer.journeyCacheMu.Unlock()

	return cachedJourney, nil
}

func journeyStopOccurrences(journey *ctdf.Journey) map[string][]int {
	occurrences := make(map[string][]int, len(journey.Path)+1)
	for index, path := range journey.Path {
		occurrences[path.OriginStopRef] = append(occurrences[path.OriginStopRef], index)
	}
	if len(journey.Path) > 0 {
		lastPath := journey.Path[len(journey.Path)-1]
		occurrences[lastPath.DestinationStopRef] = append(occurrences[lastPath.DestinationStopRef], len(journey.Path))
	}
	return occurrences
}

func loadTrackedJourney(journeyID string) (*ctdf.Journey, error) {
	var journey *ctdf.Journey
	journeysCollection := database.GetCollection("journeys")
	err := journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyID}).Decode(&journey)

	return journey, err
}

func hydrateDestinationStops(journey *ctdf.Journey) error {
	if journey == nil || len(journey.Path) == 0 {
		return nil
	}

	stopRefs := make([]string, 0, len(journey.Path))
	seenStopRefs := map[string]bool{}
	for _, pathItem := range journey.Path {
		if pathItem.DestinationStop != nil || pathItem.DestinationStopRef == "" || seenStopRefs[pathItem.DestinationStopRef] {
			continue
		}

		seenStopRefs[pathItem.DestinationStopRef] = true
		stopRefs = append(stopRefs, pathItem.DestinationStopRef)
	}

	if len(stopRefs) == 0 {
		return nil
	}

	stopsCollection := database.GetCollection("stops")
	cursor, err := stopsCollection.Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": stopRefs}},
			bson.M{"otheridentifiers": bson.M{"$in": stopRefs}},
		},
	})
	if err != nil {
		return err
	}
	defer cursor.Close(context.Background())

	stopsByIdentifier := map[string]*ctdf.Stop{}
	for cursor.Next(context.Background()) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return err
		}

		stopCopy := stop
		stopsByIdentifier[stopCopy.PrimaryIdentifier] = &stopCopy
		for _, otherIdentifier := range stopCopy.OtherIdentifiers {
			stopsByIdentifier[otherIdentifier] = &stopCopy
		}
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	for _, pathItem := range journey.Path {
		if pathItem.DestinationStop == nil {
			pathItem.DestinationStop = stopsByIdentifier[pathItem.DestinationStopRef]
		}
	}

	return nil
}

func (consumer *BatchConsumer) loadLocation(name string) *time.Location {
	if name == "" {
		return time.Local
	}

	consumer.locationCacheMu.RLock()
	location := consumer.locationCache[name]
	consumer.locationCacheMu.RUnlock()
	if location != nil {
		return location
	}

	loadedLocation, err := time.LoadLocation(name)
	if err != nil {
		log.Warn().Err(err).Str("location", name).Msg("Failed to load location")
		return time.Local
	}

	consumer.locationCacheMu.Lock()
	if consumer.locationCache == nil {
		consumer.locationCache = map[string]*time.Location{}
	}
	consumer.locationCache[name] = loadedLocation
	consumer.locationCacheMu.Unlock()

	return loadedLocation
}

func (consumer *BatchConsumer) pruneJourneyCacheLocked() {
	// Performance Review [Set A] [PR150] [P2] (tracker): Once full, inserting a new journey copies
	// and sorts 1,001 cache entries under the write lock to evict one, making each
	// eviction O(N log N) with an O(N) temporary allocation. Use an O(1) LRU or bounded
	// clock eviction structure and a monotonic access sequence instead of source
	// observation time. Measure churn, retained bytes and hit rate before raising capacity.
	if len(consumer.journeyCache) <= trackedJourneyCacheMaxEntries {
		return
	}

	type cacheEntry struct {
		journeyID string
		lastUsed  time.Time
	}

	cacheEntries := make([]cacheEntry, 0, len(consumer.journeyCache))
	for journeyID, cachedJourney := range consumer.journeyCache {
		cacheEntries = append(cacheEntries, cacheEntry{
			journeyID: journeyID,
			lastUsed:  cachedJourney.LastUsed,
		})
	}

	sort.Slice(cacheEntries, func(i int, j int) bool {
		return cacheEntries[i].lastUsed.Before(cacheEntries[j].lastUsed)
	})

	excessEntries := len(consumer.journeyCache) - trackedJourneyCacheMaxEntries
	for index := 0; index < excessEntries; index += 1 {
		delete(consumer.journeyCache, cacheEntries[index].journeyID)
	}
}
