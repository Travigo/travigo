// Package telemetry records fixed-cardinality realtime processing measurements.
package telemetry

import (
	"sync/atomic"
	"time"
)

type Stage int

const (
	Identification Stage = iota
	Cache
	Geometry
	Persistence
	Diagnostics
	Ack
	Batch
	Redis
	stageCount
)

var names = [stageCount]string{"identification", "cache", "geometry", "persistence", "diagnostics", "ack", "batch", "redis"}

type measurement struct{ count, total, max atomic.Int64 }

var stages [stageCount]measurement

func Observe(stage Stage, d time.Duration) {
	m := &stages[stage]
	m.count.Add(1)
	m.total.Add(int64(d))
	maximum(&m.max, int64(d))
}
func maximum(a *atomic.Int64, n int64) {
	for old := a.Load(); n > old; old = a.Load() {
		if a.CompareAndSwap(old, n) {
			return
		}
	}
}

type Counter int

const (
	Received Counter = iota
	Handled
	UpdateFailed
	Malformed
	Stale
	CacheHit
	CacheMiss
	CacheEvicted
	RedisCommands
	RedisErrors
	SiriGTFSSuppressedFresh
	SiriGTFSSuppressedCached
	ServiceAlerts
	ServiceAlertReferences
	ServiceAlertJourneyLookups
	counterCount
)

var counterNames = [counterCount]string{
	"received", "handled", "update_failed", "malformed", "stale", "cache_hit", "cache_miss", "cache_evicted",
	"redis_commands", "redis_errors", "siri_gtfs_suppressed_fresh", "siri_gtfs_suppressed_cached",
	"service_alerts", "service_alert_references", "service_alert_journey_lookups",
}
var counters [counterCount]atomic.Int64

func Add(c Counter, n int64) { counters[c].Add(n) }

var committedAge measurement
var age measurement
var progress atomic.Int64

func HandledEvent(recorded time.Time) {
	Add(Handled, 1)
	progress.Store(time.Now().UnixMilli())
	if !recorded.IsZero() {
		d := time.Since(recorded)
		if d < 0 {
			d = 0
		}
		age.count.Add(1)
		age.total.Add(d.Milliseconds())
		maximum(&age.max, d.Milliseconds())
	}
}

type Timing struct {
	Count   int64 `json:"count"`
	TotalNS int64 `json:"total_ns"`
	MaxNS   int64 `json:"max_ns"`
}

func timing(m *measurement) Timing { return Timing{m.count.Load(), m.total.Load(), m.max.Load()} }

type Report struct {
	Stages            map[string]Timing `json:"stages"`
	Counters          map[string]int64  `json:"counters"`
	CommittedEventAge AgeTiming         `json:"committed_event_age"`
	HandledEventAge   AgeTiming         `json:"handled_event_age"`
	LastHandledUnixMS int64             `json:"last_handled_unix_ms"`
}

func Snapshot() Report {
	r := Report{Stages: map[string]Timing{}, Counters: map[string]int64{}, HandledEventAge: ageTiming(&age), LastHandledUnixMS: progress.Load(), CommittedEventAge: ageTiming(&committedAge)}
	for i, name := range names {
		r.Stages[name] = timing(&stages[i])
	}
	for i, name := range counterNames {
		r.Counters[name] = counters[i].Load()
	}
	return r
}

var consumers [5]struct{ started, finished, batches atomic.Int64 }

func BatchStarted(id int) {
	if id >= 0 && id < len(consumers) {
		consumers[id].started.Store(time.Now().UnixMilli())
	}
}
func BatchFinished(id int) {
	if id >= 0 && id < len(consumers) {
		consumers[id].finished.Store(time.Now().UnixMilli())
		consumers[id].batches.Add(1)
	}
}

type ConsumerProgress struct {
	ID                 int   `json:"id"`
	LastStartedUnixMS  int64 `json:"last_started_unix_ms"`
	LastFinishedUnixMS int64 `json:"last_finished_unix_ms"`
	Batches            int64 `json:"batches"`
}

func Progress() []ConsumerProgress {
	result := make([]ConsumerProgress, len(consumers))
	for i := range consumers {
		c := &consumers[i]
		result[i] = ConsumerProgress{i, c.started.Load(), c.finished.Load(), c.batches.Load()}
	}
	return result
}

// Commit means the trip snapshot or location write returned success. It does
// not imply atomic location/history/event publication (covered by Set L).
func CommittedEvent(recorded time.Time) {
	if recorded.IsZero() {
		return
	}
	d := time.Since(recorded)
	if d < 0 {
		d = 0
	}
	committedAge.count.Add(1)
	committedAge.total.Add(d.Milliseconds())
	maximum(&committedAge.max, d.Milliseconds())
}

// Event ages can span days during replay; milliseconds avoid overflowing totals
// after only ~100,000 day-old observations. Stage timings remain nanoseconds.
type AgeTiming struct {
	Count   int64 `json:"count"`
	TotalMS int64 `json:"total_ms"`
	MaxMS   int64 `json:"max_ms"`
}

func ageTiming(m *measurement) AgeTiming {
	return AgeTiming{m.count.Load(), m.total.Load(), m.max.Load()}
}
