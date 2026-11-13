package telemetry

import (
	"sync/atomic"
	"time"
)

type SiriFallback int

const (
	SiriJourneyRef SiriFallback = iota
	SiriBlockRef
	SiriStops
	SiriStopsExactTime // Retained for compatibility with older reports.
	siriFallbackCount
)

var siriFallbackNames = [siriFallbackCount]string{"journey_ref", "block_ref", "stops", "stops_exact_time"}

// Disjoint buckets let interval reports estimate p95 from counter deltas.
var siriStopLookupBounds = [...]time.Duration{
	5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond,
	50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond,
	500 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second,
	10 * time.Second, 30 * time.Second,
}
var siriStopLookupCounts [len(siriStopLookupBounds) + 1]atomic.Int64

var siriFallbacks [siriFallbackCount]struct {
	lookup, narrowing              measurement
	candidates, available, matches atomic.Int64
}

type SiriFallbackReport struct {
	Lookup        Timing         `json:"lookup"`
	Narrowing     Timing         `json:"narrowing"`
	Candidates    int64          `json:"candidates"`
	Available     int64          `json:"available"`
	Matches       int64          `json:"matches"`
	LookupBuckets []LookupBucket `json:"lookup_buckets,omitempty"`
}

type LookupBucket struct {
	UpperMS int64 `json:"upper_ms,omitempty"`
	Count   int64 `json:"count"`
}

func siriStopBuckets() []LookupBucket {
	buckets := make([]LookupBucket, len(siriStopLookupCounts))
	for i, bound := range siriStopLookupBounds {
		buckets[i] = LookupBucket{UpperMS: bound.Milliseconds(), Count: siriStopLookupCounts[i].Load()}
	}
	buckets[len(buckets)-1].Count = siriStopLookupCounts[len(buckets)-1].Load()
	return buckets
}

func ObserveSiriFallback(fallback SiriFallback, lookup, narrowing time.Duration, candidates, available int, matched bool) {
	if fallback == SiriStops {
		index := 0
		for index < len(siriStopLookupBounds) && lookup > siriStopLookupBounds[index] {
			index++
		}
		siriStopLookupCounts[index].Add(1)
	}
	m := &siriFallbacks[fallback]
	observe(&m.lookup, lookup)
	observe(&m.narrowing, narrowing)
	m.candidates.Add(int64(candidates))
	m.available.Add(int64(available))
	if matched {
		m.matches.Add(1)
	}
}
