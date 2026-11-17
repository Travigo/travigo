package darwin

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func waitForTestWork(t *testing.T, wg *sync.WaitGroup) {
	t.Helper()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for dispatcher work")
	}
}

func TestDarwinDispatcherPreservesOrderPerRun(t *testing.T) {
	const workers = 4
	const runs = 8
	const updatesPerRun = 100

	dispatcher := newDarwinDispatcher(workers, 512)

	var mutex sync.Mutex
	seen := make(map[string][]int, runs)
	var wg sync.WaitGroup
	wg.Add(runs * updatesPerRun)

	for update := 0; update < updatesPerRun; update++ {
		for run := 0; run < runs; run++ {
			key := fmt.Sprintf("run-%d", run)
			sequence := update

			dispatcher.Submit(key, func() {
				mutex.Lock()
				seen[key] = append(seen[key], sequence)
				mutex.Unlock()
				wg.Done()
			})
		}
	}

	waitForTestWork(t, &wg)
	dispatcher.Close()

	for run := 0; run < runs; run++ {
		key := fmt.Sprintf("run-%d", run)
		got := seen[key]
		if len(got) != updatesPerRun {
			t.Fatalf("%s received %d updates, want %d", key, len(got), updatesPerRun)
		}
		for index, sequence := range got {
			if sequence != index {
				t.Fatalf("%s update %d has sequence %d, want %d", key, index, sequence, index)
			}
		}
	}
}

func TestDarwinDispatcherRunsDifferentRunsConcurrently(t *testing.T) {
	const workers = 4
	dispatcher := newDarwinDispatcher(workers, 32)

	firstKey := "run-a"
	secondKey := ""
	for candidate := 0; candidate < 100; candidate++ {
		key := fmt.Sprintf("run-b-%d", candidate)
		if darwinShardIndex(key, workers) != darwinShardIndex(firstKey, workers) {
			secondKey = key
			break
		}
	}
	if secondKey == "" {
		t.Fatal("could not find keys on different shards")
	}

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	dispatcher.Submit(firstKey, func() {
		started <- struct{}{}
		<-release
		wg.Done()
	})
	dispatcher.Submit(secondKey, func() {
		started <- struct{}{}
		<-release
		wg.Done()
	})

	for index := 0; index < 2; index++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			close(release)
			t.Fatal("different runs did not start concurrently")
		}
	}

	close(release)
	waitForTestWork(t, &wg)
	dispatcher.Close()
}

func TestDarwinDispatcherCloseDrainsQueuedWork(t *testing.T) {
	dispatcher := newDarwinDispatcher(2, 64)

	var completed atomic.Int64
	for index := 0; index < 64; index++ {
		dispatcher.Submit(fmt.Sprintf("run-%d", index), func() {
			completed.Add(1)
		})
	}

	dispatcher.Close()

	if completed.Load() != 64 {
		t.Fatalf("completed %d updates, want 64", completed.Load())
	}
}

func TestDarwinRunKeyStableAcrossMessageTypes(t *testing.T) {
	scheduleKey := darwinRunKey("2026-11-17", "1A23", "rid-1")
	statusKey := darwinRunKey("2026-11-17", "1A23", "rid-2")
	if scheduleKey != statusKey {
		t.Fatalf("schedule key %q differs from status key %q", scheduleKey, statusKey)
	}

	if darwinRunKey("2026-11-17", "1A24", "rid-1") == scheduleKey {
		t.Fatal("different runs share a dispatcher key")
	}

	if darwinRunKey("2026-11-17", "", "rid-1") != "run:rid:rid-1" {
		t.Fatal("RID fallback key changed")
	}

	if darwinFormationKey("rid-1") != "formation:rid-1" {
		t.Fatal("formation key changed")
	}
}

type recordingDispatcher struct {
	keys []string
	work []func()
}

func (r *recordingDispatcher) Submit(key string, work func()) {
	r.keys = append(r.keys, key)
	r.work = append(r.work, work)
}

func TestUpdateRealtimeJourneysQueuesScheduleAndStatusOnSameRunKey(t *testing.T) {
	dispatcher := &recordingDispatcher{}
	frame := &PushPortData{
		Schedules: []Schedule{{
			SSD: "2026-11-17",
			UID: "1A23",
			RID: "rid-1",
		}},
		TrainStatuses: []TrainStatus{{
			SSD: "2026-11-17",
			UID: "1A23",
			RID: "rid-1",
		}},
	}

	frame.UpdateRealtimeJourneys(dispatcher)

	if len(dispatcher.keys) != 2 {
		t.Fatalf("queued %d updates, want 2", len(dispatcher.keys))
	}
	if dispatcher.keys[0] != dispatcher.keys[1] {
		t.Fatalf("same run queued on different keys: %q and %q", dispatcher.keys[0], dispatcher.keys[1])
	}
}

func TestUpdateRealtimeJourneysQueuesFormationBeforeLoadingOnSameKey(t *testing.T) {
	dispatcher := &recordingDispatcher{}
	frame := &PushPortData{
		ScheduleFormations: []ScheduleFormations{{
			RID:        "rid-1",
			Formations: []Formation{{FID: "f1"}},
		}},
		FormationLoadings: []FormationLoading{{
			RID: "rid-1",
			FID: "f1",
		}},
	}

	frame.UpdateRealtimeJourneys(dispatcher)

	if len(dispatcher.keys) != 2 {
		t.Fatalf("queued %d updates, want 2", len(dispatcher.keys))
	}
	if dispatcher.keys[0] != dispatcher.keys[1] {
		t.Fatalf("formation and loading queued on different keys: %q and %q", dispatcher.keys[0], dispatcher.keys[1])
	}
	if dispatcher.keys[0] != "formation:rid-1" {
		t.Fatalf("formation key = %q, want formation:rid-1", dispatcher.keys[0])
	}
}
