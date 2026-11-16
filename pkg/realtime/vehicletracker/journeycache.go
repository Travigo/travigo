package vehicletracker

import (
	"container/list"
	"context"
	"fmt"
	"slices"
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
// journeys and coalesce loads. Mutable realtime state stays outside this cache.
const trackedJourneyCacheMaxEntries = 100000

var processTrackedJourneyCache = &trackedJourneyCache{}

type trackedJourneyCache struct {
	journeyCacheMu  sync.Mutex
	journeyCache    map[string]*cachedTrackedJourney
	recent, reused  list.List
	reusedBytes     int64
	loads           singleflight.Group
	bytes, maxBytes int64
}

type cachedTrackedJourney struct {
	Journey         *ctdf.Journey
	StopOccurrences map[string][]int
	StopKeys        []string
	LastUsed        time.Time
	journeyID       string
	bytes           int64
	hydrationMu     sync.Mutex
	matching        journeyView
	cachePosition   *list.Element
	reused          bool
	offsetsOnce     sync.Once
	stopOffsets     []int
	legOffsets      []int
}

func (entry *cachedTrackedJourney) callOffsets() {
	entry.offsetsOnce.Do(func() {
		for index := range entry.Journey.Calls {
			call := &entry.Journey.Calls[index]
			if !call.End || index+1 == len(entry.Journey.Calls) {
				entry.stopOffsets = append(entry.stopOffsets, index)
			}
			if index+1 < len(entry.Journey.Calls) && !call.End && !entry.Journey.Calls[index+1].Start {
				entry.legOffsets = append(entry.legOffsets, index)
			}
		}
	})
}

func (entry *cachedTrackedJourney) stopCall(journey *ctdf.Journey, index int) *ctdf.JourneyCall {
	entry.callOffsets()
	if index < 0 || index >= len(entry.stopOffsets) {
		return nil
	}
	return &journey.Calls[entry.stopOffsets[index]]
}

func (entry *cachedTrackedJourney) legEndpoints(journey *ctdf.Journey, index int) (*ctdf.JourneyCall, *ctdf.JourneyCall) {
	entry.callOffsets()
	if index < 0 || index >= len(entry.legOffsets) {
		return nil, nil
	}
	offset := entry.legOffsets[index]
	return &journey.Calls[offset], &journey.Calls[offset+1]
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

		journey, bsonBytes, err := loadTrackedJourney(journeyID)
		if err != nil {
			return nil, err
		}
		if journey == nil || len(journey.Calls) == 0 {
			return nil, fmt.Errorf("tracked journey %q has no path", journeyID)
		}
		if err := journey.ValidatePath(); err != nil {
			return nil, err
		}
		service, serviceErr := cachedTrackedService(journey.ServiceRef)
		journey.Service = service
		entry := &cachedTrackedJourney{
			Journey:         journey,
			StopOccurrences: journeyStopOccurrences(journey),
			StopKeys:        journeyDestinationKeys(journey),
			LastUsed:        now,
			journeyID:       journeyID,
			// Include decoded calls, occurrence maps, and the optional matching
			// view's copied calls. Shared stop and track caches have separate limits.
			bytes: int64(bsonBytes)*2 + int64(len(journey.Calls))*512 + 1024,
		}
		// Keep processing the current event if the service database is temporarily
		// unavailable, but retry the lookup on the next event for this journey.
		if serviceErr != nil || (cache.maxBytes > 0 && entry.bytes > cache.maxBytes) {
			return entry, nil
		}
		cache.journeyCacheMu.Lock()
		if cache.journeyCache == nil {
			cache.journeyCache = map[string]*cachedTrackedJourney{}
		}
		cache.journeyCache[journeyID] = entry
		cache.bytes += entry.bytes
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
	occurrences := make(map[string][]int, len(journey.Calls))
	for index, call := range journey.StopCalls() {
		occurrences[call.StopRef] = append(occurrences[call.StopRef], index)
	}
	return occurrences
}

func loadTrackedJourney(journeyID string) (*ctdf.Journey, int, error) {
	var journey *ctdf.Journey
	journeysCollection := database.GetCollection("journeys")
	// Read ordered calls directly from MongoDB.
	// Geometry and stops are hydrated lazily from their shared collections.
	projection := options.FindOne().SetProjection(bson.M{
		"_id": 0, "primaryidentifier": 1, "modificationdatetime": 1,
		"serviceref": 1, "operatorref": 1, "departuretime": 1, "departuretimezone": 1,
		"destinationdisplay": 1, "replacesjourneyrefs": 1, "calls": 1,
		"trackref": 1,
	})
	started := time.Now()
	result := journeysCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journeyID}, projection)
	raw, err := result.Raw()
	if err == nil {
		err = result.Decode(&journey)
		telemetry.Add(telemetry.TrackedJourneyBytes, int64(len(raw)))
	}
	telemetry.ObserveTrackedJourneyLoad(journeyID, time.Since(started))

	return journey, len(raw), err
}

type journeyView struct {
	journey *ctdf.Journey
	expires time.Time
}

func (entry *cachedTrackedJourney) journeyForMatching() *ctdf.Journey {
	entry.hydrationMu.Lock()
	defer entry.hydrationMu.Unlock()
	if entry.matching.expires.After(time.Now()) {
		return entry.matching.journey
	}

	journey := cloneJourneyPath(entry.Journey)
	stopsComplete, stopErr := hydrateDestinationStops(journey)
	if stopErr != nil {
		log.Warn().Err(stopErr).Str("journey", entry.journeyID).Msg("Failed to hydrate journey destination stops")
	}
	tracksOK, tracksComplete := hydrateJourneyTracksWithCache(journey)
	if stopErr == nil && tracksOK {
		ttl := journeyTrackMissCacheTTL
		if tracksComplete && stopsComplete {
			ttl = journeyTrackCacheTTL
		}
		entry.matching = journeyView{journey: journey, expires: time.Now().Add(ttl)}
	}
	return journey
}

func cloneJourneyPath(journey *ctdf.Journey) *ctdf.Journey {
	if journey == nil {
		return nil
	}
	clone := *journey
	clone.Calls = slices.Clone(journey.Calls)
	for index, call := range clone.Calls {
		if call.Outgoing != nil {
			leg := *call.Outgoing
			clone.Calls[index].Outgoing = &leg
		}
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

// A first read enters the recent list. A second access protects the journey
// from one pass over a large feed, within half the existing cache budget.
func (consumer *trackedJourneyCache) touchJourneyCacheLocked(entry *cachedTrackedJourney) {
	if entry.cachePosition == nil {
		entry.cachePosition = consumer.recent.PushFront(entry)
		return
	}
	if entry.reused {
		consumer.reused.MoveToFront(entry.cachePosition)
		return
	}
	consumer.recent.Remove(entry.cachePosition)
	entry.reused = true
	entry.cachePosition = consumer.reused.PushFront(entry)
	consumer.reusedBytes += entry.bytes
	for consumer.reused.Len() > trackedJourneyCacheMaxEntries/2 ||
		(consumer.maxBytes > 0 && consumer.reusedBytes > consumer.maxBytes/2) {
		oldest := consumer.reused.Back()
		demoted := oldest.Value.(*cachedTrackedJourney)
		consumer.reused.Remove(oldest)
		consumer.reusedBytes -= demoted.bytes
		demoted.reused = false
		demoted.cachePosition = consumer.recent.PushFront(demoted)
	}
}

func (consumer *trackedJourneyCache) pruneJourneyCacheLocked() {
	for len(consumer.journeyCache) > trackedJourneyCacheMaxEntries || (consumer.maxBytes > 0 && consumer.bytes > consumer.maxBytes) {
		oldest := consumer.recent.Back()
		if oldest == nil {
			oldest = consumer.reused.Back()
		}
		entry := oldest.Value.(*cachedTrackedJourney)
		if entry.reused {
			consumer.reused.Remove(oldest)
			consumer.reusedBytes -= entry.bytes
		} else {
			consumer.recent.Remove(oldest)
		}
		delete(consumer.journeyCache, entry.journeyID)
		consumer.bytes -= entry.bytes
		entry.cachePosition = nil
		entry.reused = false
		telemetry.Add(telemetry.CacheEvicted, 1)
	}
}

func journeyDestinationKeys(journey *ctdf.Journey) []string {
	keys := make([]string, 0, len(journey.Calls))
	for _, destination := range journey.LegCalls() {
		keys = append(keys, ctdf.RealtimeJourneyStopKey(destination.StopRef, len(keys)+1))
	}
	return keys
}
