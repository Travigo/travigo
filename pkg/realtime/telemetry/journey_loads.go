package telemetry

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"
	"time"
)

// Sample the same journeys in every pod so counts can be joined across pods.
// Keep only short hashes, with a fixed capacity and a count for omitted loads.
const journeyLoadSampleRate = 128
const journeyLoadSampleCapacity = 1024

var sampledJourneyLoads struct {
	sync.Mutex
	counts  map[uint64]int64
	omitted int64
}

type JourneyLoadSample struct {
	OneIn   int              `json:"one_in"`
	Counts  map[uint64]int64 `json:"counts"`
	Omitted int64            `json:"omitted"`
}

func ObserveTrackedJourneyLoad(journeyID string, elapsed time.Duration) {
	Observe(TrackedJourneyQuery, elapsed)
	hash := sha256.Sum256([]byte(journeyID))
	key := binary.BigEndian.Uint64(hash[:8])
	if key%journeyLoadSampleRate != 0 {
		return
	}

	sampledJourneyLoads.Lock()
	defer sampledJourneyLoads.Unlock()
	if sampledJourneyLoads.counts == nil {
		sampledJourneyLoads.counts = make(map[uint64]int64)
	}
	if n := sampledJourneyLoads.counts[key]; n > 0 || len(sampledJourneyLoads.counts) < journeyLoadSampleCapacity {
		sampledJourneyLoads.counts[key] = n + 1
	} else {
		sampledJourneyLoads.omitted++
	}
}

func trackedJourneyLoadSample() JourneyLoadSample {
	sampledJourneyLoads.Lock()
	defer sampledJourneyLoads.Unlock()
	counts := make(map[uint64]int64, len(sampledJourneyLoads.counts))
	for key, count := range sampledJourneyLoads.counts {
		counts[key] = count
	}
	return JourneyLoadSample{OneIn: journeyLoadSampleRate, Counts: counts, Omitted: sampledJourneyLoads.omitted}
}
