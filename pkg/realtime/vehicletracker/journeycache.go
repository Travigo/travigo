package vehicletracker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/sync/singleflight"
)

// Performance Review [Set J] [PR148] [P2] (tracker): Consumers now share static
// journeys and coalesce loads. Retained-byte limits and timetable revision
// invalidation remain follow-up work; mutable realtime state stays outside this cache.
// Preserve the former aggregate capacity of the per-consumer caches.
const trackedJourneyCacheMaxEntries = numConsumers * 5000

var processTrackedJourneyCache = &trackedJourneyCache{}

type trackedJourneyCache struct {
	journeyCacheMu                     sync.Mutex
	journeyCache                       map[string]*cachedTrackedJourney
	journeyCacheHead, journeyCacheTail *cachedTrackedJourney
	loads                              singleflight.Group
}

type cachedTrackedJourney struct {
	Journey         *ctdf.Journey
	StopOccurrences map[string][]int
	StopKeys        []string
	LastUsed        time.Time
	journeyID       string
	hydrationMu     sync.Mutex
	matching        journeyView
	previous, next  *cachedTrackedJourney
}

func (consumer *BatchConsumer) getCachedTrackedJourney(journeyID string, now time.Time) (*cachedTrackedJourney, error) {
	cache := consumer.trackedJourneyCache
	if cache == nil {
		cache = processTrackedJourneyCache
	}
	return cache.getCachedTrackedJourney(journeyID, now)
}

func (cache *trackedJourneyCache) getCachedTrackedJourney(journeyID string, now time.Time) (*cachedTrackedJourney, error) {
	started := time.Now()
	defer func() { telemetry.Observe(telemetry.Cache, time.Since(started)) }()
	cache.journeyCacheMu.Lock()
	cachedJourney := cache.journeyCache[journeyID]
	if cachedJourney != nil {
		telemetry.Add(telemetry.CacheHit, 1)
		cache.touchJourneyCacheLocked(cachedJourney)
		cache.journeyCacheMu.Unlock()
		return cachedJourney, nil
	}
	cache.journeyCacheMu.Unlock()

	telemetry.Add(telemetry.CacheMiss, 1)
	value, err, _ := cache.loads.Do(journeyID, func() (interface{}, error) {
		// A previous load may have completed between the first lookup and Do.
		cache.journeyCacheMu.Lock()
		existing := cache.journeyCache[journeyID]
		if existing != nil {
			cache.touchJourneyCacheLocked(existing)
		}
		cache.journeyCacheMu.Unlock()
		if existing != nil {
			return existing, nil
		}

		journey, err := loadTrackedJourney(journeyID)
		if err != nil {
			return nil, err
		}
		if journey == nil || len(journey.Path) == 0 {
			return nil, fmt.Errorf("tracked journey %q has no path", journeyID)
		}
		for _, path := range journey.Path {
			if path == nil {
				return nil, fmt.Errorf("tracked journey %q contains a nil path item", journeyID)
			}
		}
		journey.GetService()
		entry := &cachedTrackedJourney{
			Journey:         journey,
			StopOccurrences: journeyStopOccurrences(journey),
			StopKeys:        journeyDestinationKeys(journey),
			LastUsed:        now,
			journeyID:       journeyID,
		}
		cache.journeyCacheMu.Lock()
		if cache.journeyCache == nil {
			cache.journeyCache = map[string]*cachedTrackedJourney{}
		}
		cache.journeyCache[journeyID] = entry
		cache.touchJourneyCacheLocked(entry)
		cache.pruneJourneyCacheLocked()
		cache.journeyCacheMu.Unlock()
		return entry, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*cachedTrackedJourney), nil
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
	// Preserve geometry, embedded stops, timetable times and passenger-facing
	// fields used by realtime storage and notifications. Matching availability
	// and importer identifiers are used by the identifiers' own queries.
	projection := options.FindOne().SetProjection(bson.M{
		"_id": 0, "primaryidentifier": 1, "modificationdatetime": 1,
		"serviceref": 1, "operatorref": 1, "departuretime": 1, "departuretimezone": 1,
		"destinationdisplay": 1, "replacesjourneyrefs": 1, "path": 1,
		"track": 1, "trackref": 1,
	})
	started := time.Now()
	result := journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyID}, projection)
	raw, err := result.Raw()
	if err == nil {
		err = result.Decode(&journey)
		telemetry.Add(telemetry.TrackedJourneyBytes, int64(len(raw)))
	}
	telemetry.ObserveTrackedJourneyLoad(journeyID, time.Since(started))

	return journey, err
}

func hydrateDestinationStops(journey *ctdf.Journey, projection bson.M) error {
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
	findOptions := options.Find()
	if projection != nil {
		findOptions.SetProjection(projection)
	}
	cursor, err := stopsCollection.Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": stopRefs}},
			bson.M{"otheridentifiers": bson.M{"$in": stopRefs}},
		},
	}, findOptions)
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

type journeyView struct {
	journey *ctdf.Journey
	expires time.Time
}

func (entry *cachedTrackedJourney) journeyForMatching() *ctdf.Journey {
	return entry.hydratedJourney(&entry.matching, bson.M{"primaryidentifier": 1, "otheridentifiers": 1, "location": 1})
}

func (entry *cachedTrackedJourney) journeyForProgress() *ctdf.Journey {
	return entry.hydratedJourney(nil, nil)
}

// A nil view needs tracks only. Matching additionally loads stop coordinates
// for global-track projection and the stop fallback.
func (entry *cachedTrackedJourney) hydratedJourney(view *journeyView, projection bson.M) *ctdf.Journey {
	entry.hydrationMu.Lock()
	defer entry.hydrationMu.Unlock()
	now := time.Now()
	if view == nil {
		if entry.matching.expires.After(now) {
			return entry.matching.journey
		}
	} else if view.expires.After(now) {
		return view.journey
	}

	journey := cloneJourneyPath(entry.Journey)
	var stopErr error
	if view != nil {
		stopErr = hydrateDestinationStops(journey, projection)
		if stopErr != nil {
			log.Warn().Err(stopErr).Str("journey", entry.journeyID).Msg("Failed to hydrate journey destination stops")
		}
	}
	tracksOK, tracksComplete := hydrateJourneyTracksWithCache(journey)
	if view != nil && stopErr == nil && tracksOK {
		ttl := journeyTrackMissCacheTTL
		if tracksComplete {
			ttl = journeyTrackCacheTTL
		}
		*view = journeyView{journey: journey, expires: time.Now().Add(ttl)}
	}
	return journey
}

func cloneJourneyPath(journey *ctdf.Journey) *ctdf.Journey {
	if journey == nil {
		return nil
	}
	clone := *journey
	clone.Path = make([]*ctdf.JourneyPathItem, len(journey.Path))
	for index, path := range journey.Path {
		if path == nil {
			continue
		}
		pathClone := *path
		clone.Path[index] = &pathClone
	}
	return &clone
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

// Access order is independent of feed timestamps, which may arrive out of order.
func (consumer *trackedJourneyCache) touchJourneyCacheLocked(entry *cachedTrackedJourney) {
	if consumer.journeyCacheHead == entry {
		return
	}
	if entry.previous != nil {
		entry.previous.next = entry.next
	}
	if entry.next != nil {
		entry.next.previous = entry.previous
	}
	if consumer.journeyCacheTail == entry {
		consumer.journeyCacheTail = entry.previous
	}
	entry.previous = nil
	entry.next = consumer.journeyCacheHead
	if entry.next != nil {
		entry.next.previous = entry
	} else {
		consumer.journeyCacheTail = entry
	}
	consumer.journeyCacheHead = entry
}

func (consumer *trackedJourneyCache) pruneJourneyCacheLocked() {
	for len(consumer.journeyCache) > trackedJourneyCacheMaxEntries {
		entry := consumer.journeyCacheTail
		delete(consumer.journeyCache, entry.journeyID)
		telemetry.Add(telemetry.CacheEvicted, 1)
		consumer.journeyCacheTail = entry.previous
		if entry.previous != nil {
			entry.previous.next = nil
		} else {
			consumer.journeyCacheHead = nil
		}
		entry.previous, entry.next = nil, nil
	}
}

func journeyDestinationKeys(journey *ctdf.Journey) []string {
	keys := make([]string, len(journey.Path))
	for i, path := range journey.Path {
		keys[i] = ctdf.RealtimeJourneyStopKey(path.DestinationStopRef, i+1)
	}
	return keys
}
