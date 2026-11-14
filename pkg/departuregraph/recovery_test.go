package departuregraph

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type recoveringTopologyLoader struct {
	fakeLoader
	planningTopologyLoader
}

func TestRestoredEmptyTopologyIsRetriedWithoutLosingTimetable(t *testing.T) {
	date := time.Now()
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	data.addJourney(makeDayKey(date), testJourney())
	data.completeScan([]time.Time{date})
	data.TopologyReady = true // Reproduce the persisted production state.
	path := filepath.Join(t.TempDir(), "snapshot.zst")
	if err := graph.save(path, data); err != nil {
		t.Fatal(err)
	}
	loader := &recoveringTopologyLoader{planningTopologyLoader: planningTopologyLoader{stops: []*ctdf.Stop{
		{PrimaryIdentifier: "stop-a"}, {PrimaryIdentifier: "stop-b"}, {PrimaryIdentifier: "stop-c"},
	}}}
	restored := New(loader, Config{Enabled: true})
	if err := restored.restore(path); err != nil {
		t.Fatal(err)
	}
	if restored.current.Load().topologyReady() || restored.Stats().Journeys != 1 {
		t.Fatal("invalid topology was accepted or timetable was discarded")
	}
	if err := restored.rebuildRolling(context.Background(), date); err != nil {
		t.Fatal(err)
	}
	stats := restored.Stats()
	if !stats.TopologyReady || !stats.StaticRoutingReady || !stats.ServingToday || stats.Journeys != 1 || stats.Stops != 3 {
		t.Fatalf("graph failed to recover: %#v", stats)
	}
}

func TestEmptyStopScanFailsAndCanBeRetried(t *testing.T) {
	data := newGraphData()
	if err := data.loadTopology(context.Background(), planningTopologyLoader{}); err == nil || data.topologyReady() {
		t.Fatal("empty scan was accepted")
	}
	if err := data.loadTopology(context.Background(), planningTopologyLoader{stops: []*ctdf.Stop{{PrimaryIdentifier: "stop"}}}); err != nil || !data.topologyReady() {
		t.Fatalf("retry did not recover: %v", err)
	}
}

func TestHealthAndStatsReturnWhileGraphIsWriteLocked(t *testing.T) {
	graph := New(nil, Config{Enabled: true})
	data := graph.current.Load()
	data.addJourney(makeDayKey(time.Now()), testJourney())
	graph.Stats() // Cache counters before maintenance.
	handler := NewServer(graph).Handler()
	data.mu.Lock()
	defer data.mu.Unlock()
	for _, path := range []string{"/livez", "/healthz", "/v1/stats"} {
		done := make(chan int, 1)
		go func() {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
			done <- response.Code
		}()
		select {
		case code := <-done:
			want := http.StatusOK
			if path == "/healthz" {
				want = http.StatusServiceUnavailable
			}
			if code != want {
				t.Fatalf("%s status %d, want %d", path, code, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("%s blocked behind graph maintenance", path)
		}
	}
}

func TestInvalidJourneyTimesAreRejectedBeforeAllocation(t *testing.T) {
	for name, change := range map[string]func(*ctdf.Journey){
		"missing departure": func(j *ctdf.Journey) { j.Calls[0].Departure = time.Time{} },
		"missing arrival":   func(j *ctdf.Journey) { j.Calls[1].Arrival = time.Time{} },
		"out of bounds":     func(j *ctdf.Journey) { j.Calls[0].Departure = serviceTime(700000) },
	} {
		t.Run(name, func(t *testing.T) {
			data := newGraphData()
			journey := testJourney()
			change(journey)
			data.addJourney(20261102, journey)
			if len(data.Journeys) != 0 || len(data.Paths) != 0 || len(data.Strings) != 1 || data.stats().RejectedJourneyDays != 1 {
				t.Fatal("invalid journey retained partial graph state")
			}
		})
	}
}
