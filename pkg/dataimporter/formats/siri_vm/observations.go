package siri_vm

import (
	"crypto/sha256"
	"encoding/json"
	"maps"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/realtime/vehicletracker"
)

const (
	observationBatchSize   = 128
	recentObservationLimit = 65536
	recentObservationTTL   = 5 * time.Minute
)

type observationRun struct {
	provider, dataset, journey string
}

type observationFingerprint struct {
	recordedAt time.Time
	location   [sha256.Size]byte
}

type recentObservation struct {
	observationFingerprint
	until time.Time
}

var recentObservations = struct {
	sync.Mutex
	byRun map[observationRun]recentObservation
}{byRun: make(map[observationRun]recentObservation)}

type pendingObservation struct {
	event       *vehicletracker.VehicleUpdateEvent
	run         observationRun
	fingerprint observationFingerprint
	location    json.RawMessage
}

type observationBatch struct {
	pending                       []pendingObservation
	latest                        map[observationRun]int
	submitted, repeated, replaced int64
}

func (b *observationBatch) add(event *vehicletracker.VehicleUpdateEvent) error {
	location, err := json.Marshal(event.VehicleLocationUpdate)
	if err != nil {
		return err
	}
	observation := pendingObservation{
		event:       event,
		run:         observationRun{event.DataSource.ProviderID, event.DataSource.DatasetID, event.LocalID},
		fingerprint: observationFingerprint{event.RecordedAt.UTC(), sha256.Sum256(location)},
		location:    location,
	}
	if index, ok := b.latest[observation.run]; ok {
		previous := b.pending[index]
		if !event.RecordedAt.IsZero() && previous.fingerprint == observation.fingerprint {
			b.repeated++
			return nil
		}
		if positionsCanReplace(previous.event, event) {
			if event.RecordedAt.After(previous.event.RecordedAt) {
				b.pending[index] = observation
			}
			b.replaced++
			return nil
		}
	} else {
		recentObservations.Lock()
		previous := recentObservations.byRun[observation.run]
		recentObservations.Unlock()
		if !event.RecordedAt.IsZero() && previous.observationFingerprint == observation.fingerprint && previous.until.After(time.Now()) {
			b.repeated++
			return nil
		}
	}
	b.latest[observation.run] = len(b.pending)
	b.pending = append(b.pending, observation)
	return nil
}

func positionsCanReplace(previous, next *vehicletracker.VehicleUpdateEvent) bool {
	if previous.RecordedAt.IsZero() || next.RecordedAt.IsZero() || previous.RecordedAt.Equal(next.RecordedAt) {
		return false
	}
	a, b := previous.VehicleLocationUpdate, next.VehicleLocationUpdate
	return a.Location.Type == "Point" && b.Location.Type == "Point" &&
		len(a.StopUpdates) == 0 && len(b.StopUpdates) == 0 &&
		a.Occupancy == b.Occupancy && a.Timeframe == b.Timeframe &&
		a.VehicleIdentifier == b.VehicleIdentifier && maps.Equal(a.IdentifyingInformation, b.IdentifyingInformation)
}

func (b *observationBatch) flush(queue rmq.Queue) error {
	if len(b.pending) == 0 {
		return nil
	}
	payloads := make([][]byte, 0, len(b.pending))
	for _, observation := range b.pending {
		observation.event.SubmittedAt = time.Now()
		payload, err := json.Marshal(struct {
			*vehicletracker.VehicleUpdateEvent
			VehicleLocationUpdate json.RawMessage
		}{observation.event, observation.location})
		if err != nil {
			return err
		}
		payloads = append(payloads, payload)
	}
	if err := queue.PublishBytes(payloads...); err != nil {
		return err
	}

	until := time.Now().Add(recentObservationTTL)
	recentObservations.Lock()
	for _, observation := range b.pending {
		if _, exists := recentObservations.byRun[observation.run]; !exists && len(recentObservations.byRun) >= recentObservationLimit {
			for key := range recentObservations.byRun {
				delete(recentObservations.byRun, key)
				break
			}
		}
		recentObservations.byRun[observation.run] = recentObservation{observation.fingerprint, until}
	}
	recentObservations.Unlock()
	b.submitted += int64(len(b.pending))
	clear(b.pending)
	b.pending = b.pending[:0]
	clear(b.latest)
	return nil
}
