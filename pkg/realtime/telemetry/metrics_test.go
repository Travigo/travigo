package telemetry

import (
	"sync"
	"testing"
	"time"
)

func TestMetricsConcurrentAndBounded(t *testing.T) {
	before := Snapshot()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				Observe(Geometry, time.Millisecond)
				Add(CacheHit, 1)
			}
		}()
	}
	wg.Wait()
	after := Snapshot()
	if after.Stages["geometry"].Count-before.Stages["geometry"].Count != 1000 || after.Counters["cache_hit"]-before.Counters["cache_hit"] != 1000 {
		t.Fatal("lost measurements")
	}
	if len(after.Stages) != int(stageCount) || len(after.Counters) != int(counterCount) {
		t.Fatal("unbounded metric labels")
	}
}
func TestCommittedAgeAndProgress(t *testing.T) {
	before := Snapshot().CommittedEventAge.Count
	CommittedEvent(time.Now().Add(-time.Minute))
	CommittedEvent(time.Time{})
	if Snapshot().CommittedEventAge.Count != before+1 {
		t.Fatal("invalid commit age count")
	}
	BatchStarted(0)
	BatchFinished(0)
	BatchStarted(999999)
	if len(Progress()) != 5 || Progress()[0].LastFinishedUnixMS == 0 {
		t.Fatal("consumer progress missing")
	}
}

func TestStaleBacklogAgeDoesNotOverflow(t *testing.T) {
	const events = 110000
	recorded := time.Now().Add(-24 * time.Hour)
	before := Snapshot()
	for i := 0; i < events; i++ {
		HandledEvent(recorded)
		CommittedEvent(recorded)
	}
	after := Snapshot()
	for _, pair := range [][2]AgeTiming{{before.HandledEventAge, after.HandledEventAge}, {before.CommittedEventAge, after.CommittedEventAge}} {
		if pair[1].Count-pair[0].Count != events {
			t.Fatal("lost age samples")
		}
		delta := pair[1].TotalMS - pair[0].TotalMS
		want := int64(events) * (24 * time.Hour).Milliseconds()
		if delta < want || delta > want+int64(events)*(time.Minute).Milliseconds() {
			t.Fatalf("overflow or incorrect age units: %d", delta)
		}
		if pair[1].MaxMS < (24 * time.Hour).Milliseconds() {
			t.Fatal("incorrect maximum units")
		}
	}
}
