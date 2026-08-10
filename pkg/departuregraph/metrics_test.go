package departuregraph

import (
	"sync"
	"testing"
	"time"
)

func TestRequestTrackerSupportsConcurrentObservations(t *testing.T) {
	tracker := newRequestTracker()
	var wait sync.WaitGroup
	for index := 0; index < 100; index++ {
		wait.Add(1)
		go func(failed bool) {
			defer wait.Done()
			started := tracker.begin()
			tracker.finish(started, failed)
		}(index%10 == 0)
	}
	wait.Wait()

	stats := tracker.stats(time.Now())
	if stats.Total != 100 || stats.Completed != 100 || stats.Failed != 10 || stats.InFlight != 0 {
		t.Fatalf("unexpected concurrent request stats: %#v", stats)
	}
	if stats.CompletedLastMinute != 100 || stats.FailuresLastMinute != 10 || stats.RequestsPerSecondLastMinute <= 0 {
		t.Fatalf("unexpected rolling request stats: %#v", stats)
	}
}

func TestBuildTrackerReportsLiveProgressAndRate(t *testing.T) {
	var tracker buildTracker
	tracker.begin(4, 8)
	tracker.setEstimatedJourneys(10)
	tracker.scanned(2)
	time.Sleep(time.Millisecond)

	stats := tracker.stats()
	if !stats.Running || stats.ResumedJourneys != 4 || stats.ScannedJourneys != 5 || stats.ActiveJourneyDays != 10 || stats.Progress != 0.5 {
		t.Fatalf("unexpected live build stats: %#v", stats)
	}
	if stats.ElapsedMillis <= 0 || stats.JourneysPerSecond <= 0 || stats.EstimatedRemainingSeconds <= 0 {
		t.Fatalf("missing build performance stats: %#v", stats)
	}
}
