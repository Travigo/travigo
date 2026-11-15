package gtfs

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
)

// Bound suppression so predictions without a source timestamp can still
// refresh tracker state before the ordinary 10-minute expiry.
const gtfsObservationRefresh = 3 * time.Minute

var errGTFSObservationEncoding = errors.New("encode GTFS-RT observation")

const (
	gtfsPositionObservation = iota
	gtfsPredictionObservation
	gtfsCombinedObservation
)

type gtfsObservation struct {
	stream      [sha256.Size]byte
	content     [sha256.Size]byte
	observedAt  time.Time
	publishedAt time.Time
}

type gtfsObservationCache [64]struct {
	sync.Mutex
	entries [4096]gtfsObservation
}

// Importers are created per poll. This fixed-size process cache survives polls
// and does not add Redis work. A slot collision only loses a cache hit.
var recentGTFSObservations gtfsObservationCache

func (cache *gtfsObservationCache) publish(queue rmq.Queue, dataset string, event *vehicletracker.VehicleUpdateEvent, sourceTime, deduplicate bool, now time.Time) (bool, error) {
	location, err := json.Marshal(event.VehicleLocationUpdate)
	if err != nil {
		return false, fmt.Errorf("%w: %w", errGTFSObservationEncoding, err)
	}
	stream := sha256.Sum256([]byte(dataset + "\x00" + event.LocalID))
	index := binary.LittleEndian.Uint32(stream[:4])
	shard := &cache[index%uint32(len(cache))]
	base := int(index / uint32(len(cache)))
	entry := func(kind int) *gtfsObservation {
		return &shard.entries[(base+kind)%len(shard.entries)]
	}
	update := event.VehicleLocationUpdate
	hasPosition, hasPredictions := update.Location.Type != "", len(update.StopUpdates) != 0
	kind := gtfsPositionObservation
	if hasPredictions {
		kind = gtfsPredictionObservation
		if hasPosition {
			kind = gtfsCombinedObservation
		}
	}
	slot := entry(kind)
	var observedAt time.Time
	if sourceTime {
		observedAt = event.RecordedAt.UTC()
	}
	content := sha256.Sum256(append(location, event.MessageType...))

	shard.Lock()
	defer shard.Unlock()
	if deduplicate && slot.stream == stream && slot.content == content && slot.observedAt == observedAt && now.Before(slot.publishedAt.Add(gtfsObservationRefresh)) {
		return false, nil
	}
	submission := *event
	submission.SubmittedAt = now
	payload, err := json.Marshal(struct {
		*vehicletracker.VehicleUpdateEvent
		VehicleLocationUpdate json.RawMessage
	}{&submission, location})
	if err != nil {
		return false, fmt.Errorf("%w: %w", errGTFSObservationEncoding, err)
	}
	if err := queue.PublishBytes(payload); err != nil {
		return false, err
	}
	// Combined and lifecycle events can change both streams. A position-only
	// trip update can replace explicit predictions with GPS-based estimates.
	invalidate := func(candidate int) {
		other := entry(candidate)
		if other.stream == stream {
			*other = gtfsObservation{}
		}
	}
	invalidate(gtfsCombinedObservation)
	if !deduplicate || kind == gtfsCombinedObservation {
		invalidate(gtfsPositionObservation)
		invalidate(gtfsPredictionObservation)
	} else if kind == gtfsPositionObservation && hasPosition && event.MessageType == vehicletracker.VehicleUpdateEventTypeTrip {
		invalidate(gtfsPredictionObservation)
	}
	if deduplicate {
		*slot = gtfsObservation{stream: stream, content: content, observedAt: observedAt, publishedAt: now}
	}
	return true, nil
}
