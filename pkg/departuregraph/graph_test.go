package departuregraph

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type fakeLoader struct {
	mu        sync.Mutex
	journeys  []*ctdf.Journey
	stopLoads int
	scans     int
}

func (l *fakeLoader) LoadStopJourneys(_ context.Context, stopRefs []string, serviceDate time.Time) ([]*ctdf.Journey, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stopLoads++
	requested := make(map[string]bool, len(stopRefs))
	for _, stopRef := range stopRefs {
		requested[stopRef] = true
	}
	var result []*ctdf.Journey
	for _, journey := range l.journeys {
		if journey.Availability == nil || !journey.Availability.MatchDate(serviceDate) {
			continue
		}
		for _, path := range journey.Path {
			if path != nil && requested[path.OriginStopRef] {
				result = append(result, journey)
				break
			}
		}
	}
	return result, nil
}

func (l *fakeLoader) ScanJourneys(_ context.Context, visit func(*ctdf.Journey) error) error {
	l.mu.Lock()
	l.scans++
	journeys := append([]*ctdf.Journey(nil), l.journeys...)
	l.mu.Unlock()
	for _, journey := range journeys {
		if err := visit(journey); err != nil {
			return err
		}
	}
	return nil
}

func TestGraphLazilyFillsAndMaterializesDepartureJourneys(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	loader := &fakeLoader{journeys: []*ctdf.Journey{testJourney()}}
	graph := New(loader, Config{Enabled: true})
	stopA := &ctdf.Stop{PrimaryIdentifier: "stop-a", OtherIdentifiers: []string{"alias-a"}}

	journeys, err := graph.JourneysForStop(context.Background(), stopA, serviceDate)
	if err != nil {
		t.Fatalf("load graph stop: %v", err)
	}
	if len(journeys) != 1 {
		t.Fatalf("journeys = %d, want 1", len(journeys))
	}
	assertMaterializedJourney(t, journeys[0])

	if _, err := graph.JourneysForStop(context.Background(), stopA, serviceDate); err != nil {
		t.Fatalf("load completed graph stop: %v", err)
	}
	if loader.stopLoads != 1 {
		t.Fatalf("stop loads = %d, want 1", loader.stopLoads)
	}

	// The first journey already populated stop-b's departure bucket, but it is
	// not considered complete until stop-b itself has been queried or a full
	// background scan has completed.
	stopB := &ctdf.Stop{PrimaryIdentifier: "stop-b"}
	journeys, err = graph.JourneysForStop(context.Background(), stopB, serviceDate)
	if err != nil {
		t.Fatalf("load propagated graph stop: %v", err)
	}
	if len(journeys) != 1 || loader.stopLoads != 2 {
		t.Fatalf("propagated stop journeys=%d loads=%d, want 1 and 2", len(journeys), loader.stopLoads)
	}
}

func TestGraphSnapshotRestoresCompletedLazyFill(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	loader := &fakeLoader{journeys: []*ctdf.Journey{testJourney()}}
	graph := New(loader, Config{Enabled: true})
	stop := &ctdf.Stop{PrimaryIdentifier: "stop-a"}
	if _, err := graph.JourneysForStop(context.Background(), stop, serviceDate); err != nil {
		t.Fatalf("fill graph: %v", err)
	}

	path := filepath.Join(t.TempDir(), "departure-graph.gob.zst")
	if err := graph.save(path, graph.current.Load()); err != nil {
		t.Fatalf("save graph: %v", err)
	}

	restoredLoader := &fakeLoader{}
	restored := New(restoredLoader, Config{Enabled: true})
	if err := restored.restore(path); err != nil {
		t.Fatalf("restore graph: %v", err)
	}
	journeys, err := restored.JourneysForStop(context.Background(), stop, serviceDate)
	if err != nil {
		t.Fatalf("read restored graph: %v", err)
	}
	if len(journeys) != 1 || restoredLoader.stopLoads != 0 {
		t.Fatalf("restored journeys=%d loads=%d, want 1 and 0", len(journeys), restoredLoader.stopLoads)
	}
	assertMaterializedJourney(t, journeys[0])
}

func TestBackgroundRebuildCompletesRollingDays(t *testing.T) {
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	loader := &fakeLoader{journeys: []*ctdf.Journey{testJourney()}}
	graph := New(loader, Config{
		Enabled:    true,
		DaysBehind: 1,
		DaysAhead:  1,
		BatchSize:  1000,
	})
	if err := graph.rebuildRolling(context.Background(), now); err != nil {
		t.Fatalf("rebuild graph: %v", err)
	}

	for _, date := range rollingDates(now, 1, 1) {
		journeys, err := graph.JourneysForStop(context.Background(), &ctdf.Stop{PrimaryIdentifier: "stop-a"}, date)
		if err != nil {
			t.Fatalf("read completed day %s: %v", date, err)
		}
		if len(journeys) != 1 {
			t.Fatalf("journeys on %s = %d, want 1", date.Format("2006-01-02"), len(journeys))
		}
	}
	if loader.stopLoads != 0 || loader.scans != 1 {
		t.Fatalf("stop loads=%d scans=%d, want 0 and 1", loader.stopLoads, loader.scans)
	}
}

func TestBackgroundBuildRetainsRestoredPartialGeneration(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	loader := &fakeLoader{journeys: []*ctdf.Journey{testJourney()}}
	graph := New(loader, Config{Enabled: true})
	stop := &ctdf.Stop{PrimaryIdentifier: "stop-a"}
	if _, err := graph.JourneysForStop(context.Background(), stop, serviceDate); err != nil {
		t.Fatalf("fill partial graph: %v", err)
	}

	path := filepath.Join(t.TempDir(), "departure-graph.gob.zst")
	if err := graph.save(path, graph.current.Load()); err != nil {
		t.Fatalf("save partial graph: %v", err)
	}
	restoredLoader := &fakeLoader{}
	restored := New(restoredLoader, Config{Enabled: true, BatchSize: 1000})
	if err := restored.restore(path); err != nil {
		t.Fatalf("restore partial graph: %v", err)
	}
	if err := restored.rebuildRolling(context.Background(), serviceDate); err != nil {
		t.Fatalf("continue background build: %v", err)
	}

	journeys, err := restored.JourneysForStop(context.Background(), stop, serviceDate)
	if err != nil {
		t.Fatalf("read continued graph: %v", err)
	}
	if len(journeys) != 1 || restoredLoader.stopLoads != 0 {
		t.Fatalf("continued journeys=%d stop loads=%d, want 1 and 0", len(journeys), restoredLoader.stopLoads)
	}
}

func testJourney() *ctdf.Journey {
	serviceStart := time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC)
	return &ctdf.Journey{
		PrimaryIdentifier:   "journey-1",
		OtherIdentifiers:    map[string]string{"BlockNumber": "block-7"},
		DataSource:          &ctdf.DataSourceReference{DatasetID: "dataset-1"},
		ServiceRef:          "service-1",
		OperatorRef:         "operator-1",
		DepartureTime:       serviceStart.Add(25 * time.Hour),
		DepartureTimezone:   "Europe/London",
		DestinationDisplay:  "Central",
		ReplacesJourneyRefs: []string{"journey-old"},
		Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{
			Type: ctdf.AvailabilityMatchAll,
		}}},
		Path: []*ctdf.JourneyPathItem{
			{
				OriginStopRef:          "stop-a",
				DestinationStopRef:     "stop-b",
				OriginPlatform:         "1",
				DestinationPlatform:    "2",
				OriginDepartureTime:    serviceStart.Add(25*time.Hour + 5*time.Minute),
				DestinationArrivalTime: serviceStart.Add(25*time.Hour + 20*time.Minute),
				DestinationDisplay:     "Central",
				OriginActivity:         []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup},
				DestinationActivity:    []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown},
			},
			{
				OriginStopRef:          "stop-b",
				DestinationStopRef:     "stop-c",
				OriginDepartureTime:    serviceStart.Add(25*time.Hour + 25*time.Minute),
				DestinationArrivalTime: serviceStart.Add(25*time.Hour + 45*time.Minute),
			},
		},
	}
}

func assertMaterializedJourney(t *testing.T, journey *ctdf.Journey) {
	t.Helper()
	if journey.PrimaryIdentifier != "journey-1" || journey.ServiceRef != "service-1" || journey.OperatorRef != "operator-1" {
		t.Fatalf("unexpected materialized identity: %#v", journey)
	}
	if journey.OtherIdentifiers["BlockNumber"] != "block-7" || journey.DataSource == nil || journey.DataSource.DatasetID != "dataset-1" {
		t.Fatalf("materialized block metadata missing: %#v", journey)
	}
	if len(journey.Path) != 2 || journey.Path[0].OriginDepartureTime.Hour() != 1 || journey.Path[0].OriginDepartureTime.Day() != 2 {
		t.Fatalf("service-day overflow not preserved: %#v", journey.Path)
	}
	if len(journey.Path[0].OriginActivity) != 1 || journey.Path[0].OriginActivity[0] != ctdf.JourneyPathItemActivityPickup {
		t.Fatalf("pickup activity not preserved: %#v", journey.Path[0].OriginActivity)
	}
	if !journey.Availability.MatchDate(time.Now()) {
		t.Fatal("materialized journey should be active for its already-selected service day")
	}
}
