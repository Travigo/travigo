package vehicletracker

import (
	"container/list"
	"math"
	"runtime"
	"sync"
	"weak"

	"github.com/travigo/travigo/pkg/ctdf"
)

const stopProjectionCacheMaxEntries = 65536

// A weak reference distinguishes replacement generations of a shared TrackRef
// without keeping the track's backing array alive after journey and track-cache
// eviction. Track slices are immutable once hydrated.
type stopProjectionShape struct {
	trackRef   string
	trackStart weak.Pointer[ctdf.Location]
	trackLen   int
}

type stopProjectionKey struct {
	shape     stopProjectionShape
	longitude uint64
	latitude  uint64
}

type stopProjection struct {
	key      stopProjectionKey
	once     sync.Once
	progress float64
	valid    bool
}

type stopProjectionCache struct {
	mu      sync.Mutex
	entries map[stopProjectionKey]*list.Element
	lru     list.List
}

var globalStopProjectionCache stopProjectionCache

func newStopProjectionShape(trackRef string, track []ctdf.Location) stopProjectionShape {
	shape := stopProjectionShape{trackRef: trackRef, trackLen: len(track)}
	if len(track) > 0 {
		shape.trackStart = weak.Make(&track[0])
	}
	return shape
}

func cachedStopProgress(shape stopProjectionShape, stop ctdf.Location, track []ctdf.Location) (float64, bool) {
	if len(track) < 2 || len(stop.Coordinates) < 2 {
		// Invalid stops used to reach projectOntoTrackSegment with too few
		// coordinates and panic. Skip them consistently with malformed track
		// vertices, which closestTrackPosition already ignores.
		if len(stop.Coordinates) < 2 {
			return 0, false
		}
		_, progress, ok := closestTrackPosition(stop, track)
		return progress, ok
	}

	key := stopProjectionKey{
		shape:     shape,
		longitude: math.Float64bits(stop.Coordinates[0]),
		latitude:  math.Float64bits(stop.Coordinates[1]),
	}

	cache := &globalStopProjectionCache
	cache.mu.Lock()
	var projection *stopProjection
	if element := cache.entries[key]; element != nil {
		cache.lru.MoveToFront(element)
		projection = element.Value.(*stopProjection)
	} else {
		if cache.entries == nil {
			cache.entries = make(map[stopProjectionKey]*list.Element)
		}
		projection = &stopProjection{key: key}
		cache.entries[key] = cache.lru.PushFront(projection)
		if cache.lru.Len() > stopProjectionCacheMaxEntries {
			oldest := cache.lru.Back()
			delete(cache.entries, oldest.Value.(*stopProjection).key)
			cache.lru.Remove(oldest)
		}
	}
	cache.mu.Unlock()

	// Cold workers requesting the same stop and shape share one geometry scan.
	projection.once.Do(func() {
		_, projection.progress, projection.valid = closestTrackPosition(stop, track)
	})
	runtime.KeepAlive(track)
	return projection.progress, projection.valid
}
