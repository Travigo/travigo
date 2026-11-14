package vehicletracker

import (
	"container/list"
	"runtime"
	"sync"

	"github.com/travigo/travigo/pkg/ctdf"
)

// Tracks are immutable once hydrated. Weak keys do not retain their backing
// arrays, and both entry count and cumulative-length memory are bounded.
const trackLengthCacheMaxBytes = 16 << 20
const trackLengthCacheMaxEntries = 1024

type trackLengths struct {
	shape      stopProjectionShape
	cumulative []float64
}

var trackLengthCache struct {
	sync.Mutex
	entries map[stopProjectionShape]*list.Element
	lru     list.List
	bytes   int
}

func cachedTrackLengths(track []ctdf.Location) []float64 {
	if len(track) < 2 || len(track) > trackLengthCacheMaxBytes/8 {
		return nil
	}
	shape := newStopProjectionShape("", track)
	trackLengthCache.Lock()
	if element := trackLengthCache.entries[shape]; element != nil {
		trackLengthCache.lru.MoveToFront(element)
		lengths := element.Value.(trackLengths).cumulative
		trackLengthCache.Unlock()
		runtime.KeepAlive(track)
		return lengths
	}
	trackLengthCache.Unlock()
	lengths := make([]float64, len(track))
	for i := 1; i < len(track); i++ {
		lengths[i] = lengths[i-1]
		if len(track[i-1].Coordinates) >= 2 && len(track[i].Coordinates) >= 2 {
			lengths[i] += track[i-1].Distance(&track[i])
		}
	}
	trackLengthCache.Lock()
	defer trackLengthCache.Unlock()
	if element := trackLengthCache.entries[shape]; element != nil {
		trackLengthCache.lru.MoveToFront(element)
		runtime.KeepAlive(track)
		return element.Value.(trackLengths).cumulative
	}
	if trackLengthCache.entries == nil {
		trackLengthCache.entries = make(map[stopProjectionShape]*list.Element)
	}
	for trackLengthCache.bytes+len(lengths)*8 > trackLengthCacheMaxBytes || trackLengthCache.lru.Len() >= trackLengthCacheMaxEntries {
		oldest := trackLengthCache.lru.Back()
		entry := oldest.Value.(trackLengths)
		delete(trackLengthCache.entries, entry.shape)
		trackLengthCache.bytes -= len(entry.cumulative) * 8
		trackLengthCache.lru.Remove(oldest)
	}
	trackLengthCache.entries[shape] = trackLengthCache.lru.PushFront(trackLengths{shape, lengths})
	trackLengthCache.bytes += len(lengths) * 8
	runtime.KeepAlive(track)
	return lengths
}
