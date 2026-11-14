package vehicletracker

import (
	"container/list"
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"golang.org/x/sync/singleflight"
)

const (
	journeyTrackCacheMaxEntries = 1024
	journeyTrackCacheMaxBytes   = 64 << 20
	journeyTrackCacheTTL        = 5 * time.Minute
	journeyTrackMissCacheTTL    = 15 * time.Second
)

type journeyTrackCacheEntry struct {
	reference string
	track     ctdf.JourneyTrack
	expiresAt time.Time
	bytes     int64
	missing   bool
}

type journeyTrackCache struct {
	mu      sync.Mutex
	entries map[string]*list.Element
	lru     list.List
	bytes   int64
}

var (
	processJourneyTrackCache journeyTrackCache
	journeyTrackFetches      singleflight.Group
)

func hydrateJourneyTracksWithCache(journey *ctdf.Journey) (bool, bool) {
	if journey == nil {
		return false, false
	}
	refs := make([]string, 0, len(journey.Calls)+1)
	seen := make(map[string]struct{}, len(journey.Calls)+1)
	if journey.TrackRef != "" {
		refs = append(refs, journey.TrackRef)
		seen[journey.TrackRef] = struct{}{}
	}
	for _, call := range journey.Calls {
		path := call.Outgoing
		if path == nil || path.TrackRef == "" {
			continue
		}
		if _, ok := seen[path.TrackRef]; ok {
			continue
		}
		seen[path.TrackRef] = struct{}{}
		refs = append(refs, path.TrackRef)
	}
	if len(refs) == 0 {
		return true, true
	}

	tracks := make(map[string]ctdf.JourneyTrack, len(refs))
	missingRefs := make([]string, 0, len(refs))
	now := time.Now()
	for _, reference := range refs {
		track, cached, missing := processJourneyTrackCache.get(reference, now)
		if !cached {
			missingRefs = append(missingRefs, reference)
		} else if !missing {
			tracks[reference] = track
		}
	}

	var queryErr error
	if len(missingRefs) > 0 {
		loadedTracks, err := fetchJourneyTracksCoalesced(missingRefs)
		queryErr = err
		for _, reference := range missingRefs {
			track, found := loadedTracks[reference]
			if !found {
				if err == nil {
					track, found = processJourneyTrackCache.put(reference, ctdf.JourneyTrack{}, true, time.Now())
				} else if cachedTrack, cached, missing := processJourneyTrackCache.get(reference, time.Now()); cached && !missing {
					track, found = cachedTrack, true
				}
			}
			if found {
				track, _ = processJourneyTrackCache.put(reference, track, false, time.Now())
				tracks[reference] = track
			}
		}
	}

	if queryErr != nil {
		log.Warn().Err(queryErr).Str("journey", journey.PrimaryIdentifier).Int("track_references", len(missingRefs)).Msg("Failed to hydrate journey tracks")
	}
	applyCachedJourneyTracks(journey, tracks)
	return queryErr == nil, len(tracks) == len(refs)
}

func applyCachedJourneyTracks(journey *ctdf.Journey, tracks map[string]ctdf.JourneyTrack) {
	if journey.TrackRef != "" {
		if track, ok := tracks[journey.TrackRef]; ok {
			journey.Track = track.Track
			journey.TrackDataSource = track.DataSource
		}
	}
	for _, call := range journey.Calls {
		path := call.Outgoing
		if path == nil || path.TrackRef == "" {
			continue
		}
		if track, ok := tracks[path.TrackRef]; ok {
			path.Track = track.Track
			path.TrackDataSource = track.DataSource
		}
	}
}

func fetchJourneyTracksCoalesced(refs []string) (map[string]ctdf.JourneyTrack, error) {
	keyRefs := append([]string(nil), refs...)
	sort.Strings(keyRefs)
	key := strings.Join(keyRefs, "\x00")
	result, err, _ := journeyTrackFetches.Do(key, func() (interface{}, error) {
		return loadJourneyTracks(refs)
	})
	if result == nil {
		return nil, err
	}
	return result.(map[string]ctdf.JourneyTrack), err
}

func loadJourneyTracks(refs []string) (map[string]ctdf.JourneyTrack, error) {
	cursor, err := database.GetCollection("journey_tracks").Find(context.Background(), bson.M{"primaryidentifier": bson.M{"$in": refs}})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	tracks := make(map[string]ctdf.JourneyTrack, len(refs))
	var loadErr error
	for cursor.Next(context.Background()) {
		var track ctdf.JourneyTrack
		if err := cursor.Decode(&track); err != nil {
			if loadErr == nil {
				loadErr = err
			}
			continue
		}
		tracks[track.PrimaryIdentifier] = track
	}
	if err := cursor.Err(); err != nil && loadErr == nil {
		loadErr = err
	}
	return tracks, loadErr
}

func (cache *journeyTrackCache) get(reference string, now time.Time) (ctdf.JourneyTrack, bool, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		return ctdf.JourneyTrack{}, false, false
	}
	element := cache.entries[reference]
	if element == nil {
		return ctdf.JourneyTrack{}, false, false
	}
	entry := element.Value.(*journeyTrackCacheEntry)
	if !entry.expiresAt.After(now) {
		cache.removeLocked(element)
		return ctdf.JourneyTrack{}, false, false
	}
	cache.lru.MoveToFront(element)
	return entry.track, true, entry.missing
}

// put returns the retained positive entry when a concurrent fetch has already
// populated it; an in-flight miss never replaces a still-fresh positive value.
func (cache *journeyTrackCache) put(reference string, track ctdf.JourneyTrack, missing bool, now time.Time) (ctdf.JourneyTrack, bool) {
	entryBytes := int64(64)
	ttl := journeyTrackMissCacheTTL
	if !missing {
		entryBytes = journeyTrackApproxBytes(track)
		ttl = journeyTrackCacheTTL
		if entryBytes > journeyTrackCacheMaxBytes {
			return track, true
		}
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.entries == nil {
		cache.entries = make(map[string]*list.Element)
	}
	if existing := cache.entries[reference]; existing != nil {
		current := existing.Value.(*journeyTrackCacheEntry)
		if current.expiresAt.After(now) {
			if !current.missing {
				if !missing && track.ModificationDateTime.After(current.track.ModificationDateTime) {
					cache.removeLocked(existing)
				} else {
					cache.lru.MoveToFront(existing)
					return current.track, true
				}
			}
			if missing {
				cache.lru.MoveToFront(existing)
				return ctdf.JourneyTrack{}, false
			}
		}
		if cache.entries[reference] == existing {
			cache.removeLocked(existing)
		}
	}
	entry := &journeyTrackCacheEntry{
		reference: reference,
		track:     track,
		expiresAt: now.Add(ttl),
		bytes:     entryBytes,
		missing:   missing,
	}
	cache.entries[reference] = cache.lru.PushFront(entry)
	cache.bytes += entryBytes
	cache.pruneLocked()
	return track, !missing
}

func (cache *journeyTrackCache) pruneLocked() {
	for len(cache.entries) > journeyTrackCacheMaxEntries || cache.bytes > journeyTrackCacheMaxBytes {
		oldest := cache.lru.Back()
		if oldest == nil {
			return
		}
		cache.removeLocked(oldest)
	}
}

func (cache *journeyTrackCache) removeLocked(element *list.Element) {
	entry := element.Value.(*journeyTrackCacheEntry)
	delete(cache.entries, entry.reference)
	cache.bytes -= entry.bytes
	cache.lru.Remove(element)
}

func journeyTrackApproxBytes(track ctdf.JourneyTrack) int64 {
	// Include coordinate arrays and strings, plus a conservative allowance for
	// each Location, slice header, track record, and datasource.
	bytes := int64(256+len(track.PrimaryIdentifier)) + int64(cap(track.Track))*48
	if track.DataSource != nil {
		bytes += int64(len(track.DataSource.DatasetID) + len(track.DataSource.ProviderID) + len(track.DataSource.ProviderName) + len(track.DataSource.OriginalFormat) + len(track.DataSource.Timestamp) + 128)
	}
	for _, location := range track.Track {
		bytes += int64(len(location.Type) + 8*cap(location.Coordinates))
	}
	return bytes
}
