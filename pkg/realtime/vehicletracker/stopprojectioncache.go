package vehicletracker

import (
	"container/list"
	"math"
	"runtime"
	"sync"
	"weak"

	"github.com/travigo/travigo/pkg/ctdf"
)

const stopProjectionCacheMaxEntries = 4096

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

	globalStopProjectionCache.mu.Lock()
	if element := globalStopProjectionCache.entries[key]; element != nil {
		globalStopProjectionCache.lru.MoveToFront(element)
		projection := element.Value.(stopProjection)
		globalStopProjectionCache.mu.Unlock()
		runtime.KeepAlive(track)
		return projection.progress, projection.valid
	}
	globalStopProjectionCache.mu.Unlock()

	_, progress, valid := closestTrackPosition(stop, track)
	globalStopProjectionCache.mu.Lock()
	defer globalStopProjectionCache.mu.Unlock()
	if element := globalStopProjectionCache.entries[key]; element != nil {
		globalStopProjectionCache.lru.MoveToFront(element)
		projection := element.Value.(stopProjection)
		runtime.KeepAlive(track)
		return projection.progress, projection.valid
	}
	if globalStopProjectionCache.entries == nil {
		globalStopProjectionCache.entries = make(map[stopProjectionKey]*list.Element)
	}
	element := globalStopProjectionCache.lru.PushFront(stopProjection{key: key, progress: progress, valid: valid})
	globalStopProjectionCache.entries[key] = element
	if globalStopProjectionCache.lru.Len() > stopProjectionCacheMaxEntries {
		oldest := globalStopProjectionCache.lru.Back()
		delete(globalStopProjectionCache.entries, oldest.Value.(stopProjection).key)
		globalStopProjectionCache.lru.Remove(oldest)
	}
	// Keep the array live until after the weak key has been created and the
	// projection is complete, but never retain it in the cache itself.
	runtime.KeepAlive(track)
	return progress, valid
}
