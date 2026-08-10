package departuregraph

import (
	"context"
	"errors"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type fakeLoader struct {
	mu            sync.Mutex
	journeys      []*ctdf.Journey
	stopLoads     int
	scans         int
	failScanAfter int
	scanVisits    map[string]int
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

func (l *fakeLoader) ScanJourneys(_ context.Context, after string, visit func(*ctdf.Journey, string) error) error {
	l.mu.Lock()
	l.scans++
	journeys := append([]*ctdf.Journey(nil), l.journeys...)
	failAfter := l.failScanAfter
	l.failScanAfter = 0
	l.mu.Unlock()
	start, _ := strconv.Atoi(after)
	for index := start; index < len(journeys); index++ {
		if failAfter > 0 && index-start == failAfter {
			return errors.New("interrupted scan")
		}
		journey := journeys[index]
		l.mu.Lock()
		if l.scanVisits == nil {
			l.scanVisits = map[string]int{}
		}
		l.scanVisits[journey.PrimaryIdentifier]++
		l.mu.Unlock()
		if err := visit(journey, strconv.Itoa(index+1)); err != nil {
			return err
		}
	}
	return nil
}

func (l *fakeLoader) JourneyCount(_ context.Context) (int64, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return int64(len(l.journeys)), nil
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

func TestGraphColdFillReturnsBeforeAsynchronousInsertionCompletes(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	graph := New(&fakeLoader{journeys: []*ctdf.Journey{testJourney()}}, Config{Enabled: true})
	fillStarted := make(chan struct{})
	releaseFill := make(chan struct{})
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseFill) }) }
	defer release()
	graph.beforeApplyLazyFill = func() {
		close(fillStarted)
		<-releaseFill
	}

	stop := &ctdf.Stop{PrimaryIdentifier: "stop-a"}
	journeys, err := graph.JourneysForStopWindow(context.Background(), stop, serviceDate, time.Time{}, 1)
	if err != nil {
		t.Fatalf("load cold graph stop: %v", err)
	}
	if len(journeys) != 1 {
		t.Fatalf("cold graph journeys = %d, want 1", len(journeys))
	}
	select {
	case <-fillStarted:
	case <-time.After(time.Second):
		t.Fatal("asynchronous graph insertion did not start")
	}
	if graph.current.Load().stopComplete(makeDayKey(serviceDate), stop.PrimaryIdentifier) {
		t.Fatal("cold request waited for graph insertion to complete")
	}

	release()
	waitForStopComplete(t, graph, stop, serviceDate)
}

func TestGraphWindowFiltersAndSortsByDepartureAtRequestedStop(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	serviceStart := time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC)
	journeyAt := func(identifier string, departureHour int) *ctdf.Journey {
		return &ctdf.Journey{
			PrimaryIdentifier: identifier,
			Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{
				Type: ctdf.AvailabilityMatchAll,
			}}},
			Path: []*ctdf.JourneyPathItem{{
				OriginStopRef:          "stop-a",
				DestinationStopRef:     "stop-b",
				OriginDepartureTime:    serviceStart.Add(time.Duration(departureHour) * time.Hour),
				DestinationArrivalTime: serviceStart.Add(time.Duration(departureHour)*time.Hour + 30*time.Minute),
			}},
		}
	}
	graph := New(&fakeLoader{}, Config{Enabled: true})
	data := graph.current.Load()
	data.addJourneys(makeDayKey(serviceDate), []*ctdf.Journey{
		journeyAt("journey-11", 11),
		journeyAt("journey-09", 9),
		journeyAt("journey-10", 10),
	})
	data.markStopComplete(makeDayKey(serviceDate), "stop-a")

	journeys, err := graph.JourneysForStopWindow(
		context.Background(),
		&ctdf.Stop{PrimaryIdentifier: "stop-a"},
		serviceDate,
		serviceDate.Add(9*time.Hour+30*time.Minute),
		1,
	)
	if err != nil {
		t.Fatalf("load graph window: %v", err)
	}
	if len(journeys) != 1 || journeys[0].PrimaryIdentifier != "journey-10" {
		t.Fatalf("window journeys = %#v, want journey-10", journeys)
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
	waitForStopComplete(t, graph, stop, serviceDate)

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
	stats := graph.Stats().BackgroundBuild
	if stats.Running || stats.EstimatedJourneys != 1 || stats.ScannedJourneys != 1 || stats.Progress != 1 || stats.SuccessfulBuilds != 1 {
		t.Fatalf("unexpected background build stats: %#v", stats)
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
	waitForStopComplete(t, graph, stop, serviceDate)

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

func TestBackgroundBuildResumesFromSnapshotCursor(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	journeys := []*ctdf.Journey{
		testJourneyWithID("journey-1"),
		testJourneyWithID("journey-2"),
		testJourneyWithID("journey-3"),
	}
	loader := &fakeLoader{journeys: journeys, failScanAfter: 2}
	graph := New(loader, Config{Enabled: true, BatchSize: 1})
	if err := graph.rebuildRolling(context.Background(), serviceDate); err == nil {
		t.Fatal("expected interrupted background build")
	}
	if stats := graph.Stats().BackgroundBuild; stats.ScannedJourneys != 2 || stats.FailedBuilds != 1 {
		t.Fatalf("unexpected interrupted build stats: %#v", stats)
	}

	path := filepath.Join(t.TempDir(), "departure-graph.gob.zst")
	if err := graph.save(path, graph.current.Load()); err != nil {
		t.Fatalf("save interrupted graph: %v", err)
	}

	resumedLoader := &fakeLoader{journeys: journeys}
	resumed := New(resumedLoader, Config{Enabled: true, BatchSize: 1})
	if err := resumed.restore(path); err != nil {
		t.Fatalf("restore interrupted graph: %v", err)
	}
	if err := resumed.rebuildRolling(context.Background(), serviceDate); err != nil {
		t.Fatalf("resume background graph: %v", err)
	}

	if resumedLoader.scanVisits["journey-1"] != 0 || resumedLoader.scanVisits["journey-2"] != 0 || resumedLoader.scanVisits["journey-3"] != 1 {
		t.Fatalf("resume revisited checkpointed journeys: %#v", resumedLoader.scanVisits)
	}
	stats := resumed.Stats()
	if stats.Journeys != 3 || stats.CompleteDays != 1 {
		t.Fatalf("unexpected resumed graph stats: %#v", stats)
	}
	if stats.BackgroundBuild.ResumedJourneys != 2 || stats.BackgroundBuild.ScannedJourneys != 3 || stats.BackgroundBuild.SuccessfulBuilds != 1 {
		t.Fatalf("unexpected resumed build stats: %#v", stats.BackgroundBuild)
	}
}

func TestBackgroundBuildFailureUsesRetryInterval(t *testing.T) {
	graph := New(&fakeLoader{}, Config{
		Enabled:         true,
		RefreshInterval: 24 * time.Hour,
		RetryInterval:   2 * time.Minute,
	})
	if wait := graph.waitAfterBuild(errors.New("failed"), 30*time.Minute); wait != 2*time.Minute {
		t.Fatalf("failed build wait = %s, want 2m", wait)
	}
	if wait := graph.waitAfterBuild(nil, 30*time.Minute); wait != 23*time.Hour+30*time.Minute {
		t.Fatalf("successful build wait = %s, want 23h30m", wait)
	}
}

func TestConfigFromEnvironmentReadsRetryInterval(t *testing.T) {
	config := ConfigFromEnvironment(map[string]string{
		"TRAVIGO_DEPARTURE_GRAPH_RETRY_INTERVAL": "3m",
	})
	if config.RetryInterval != 3*time.Minute {
		t.Fatalf("retry interval = %s, want 3m", config.RetryInterval)
	}
}

func TestSnapshotStatsTrackWritesAndRestore(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "departure-graph.gob.zst")
	graph := New(&fakeLoader{journeys: []*ctdf.Journey{testJourney()}}, Config{
		Enabled:      true,
		SnapshotPath: path,
	})
	if _, err := graph.JourneysForStop(context.Background(), &ctdf.Stop{PrimaryIdentifier: "stop-a"}, serviceDate); err != nil {
		t.Fatalf("fill graph: %v", err)
	}
	waitForStopComplete(t, graph, &ctdf.Stop{PrimaryIdentifier: "stop-a"}, serviceDate)
	if err := graph.Save(); err != nil {
		t.Fatalf("save graph: %v", err)
	}
	written := graph.Stats().Snapshot
	if written.SuccessfulWrites != 1 || written.FailedWrites != 0 || written.FileSizeBytes <= 0 || written.LastWriteAt == nil {
		t.Fatalf("unexpected snapshot write stats: %#v", written)
	}

	restored := New(&fakeLoader{}, Config{Enabled: true, SnapshotPath: path})
	if err := restored.restoreTracked(path); err != nil {
		t.Fatalf("restore graph: %v", err)
	}
	restoreStats := restored.Stats().Snapshot
	if restoreStats.RestoredAt == nil || restoreStats.FileSizeBytes != written.FileSizeBytes || restoreStats.LastRestoreError != "" {
		t.Fatalf("unexpected snapshot restore stats: %#v", restoreStats)
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

func waitForStopComplete(t *testing.T, graph *Graph, stop *ctdf.Stop, serviceDate time.Time) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !graph.current.Load().stopComplete(makeDayKey(serviceDate), stop.PrimaryIdentifier) {
		if time.Now().After(deadline) {
			t.Fatalf("graph stop %s did not complete", stop.PrimaryIdentifier)
		}
		time.Sleep(time.Millisecond)
	}
}

func testJourneyWithID(identifier string) *ctdf.Journey {
	journey := testJourney()
	journey.PrimaryIdentifier = identifier
	return journey
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
