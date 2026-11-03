package localdepartureboard

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/liip/sheriff"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/departuregraph"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestLegacyVSTPCancellationFallsBackOnlyWithoutDaySpecificAlert(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("legacy and alert-backed cancellations", func(mt *mtest.T) {
		previous := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previous })
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".service_alerts", mtest.FirstBatch,
			bson.D{{Key: "primaryidentifier", Value: "gb-networkrail-vstpcancel-2026-11-03:2026-11-05:new"}},
		))
		realtime := map[string]*ctdf.RealtimeJourney{
			"old":     {PrimaryIdentifier: "gb-nationalrail-vstp-cancellation-2026-11-03:2026-11-05:old", Cancelled: true},
			"new":     {PrimaryIdentifier: "gb-nationalrail-vstp-cancellation-2026-11-03:2026-11-05:new", Cancelled: true},
			"outside": {PrimaryIdentifier: "gb-nationalrail-vstp-cancellation-2026-11-01:2026-11-02:outside", Cancelled: true},
			"other":   {PrimaryIdentifier: "unrelated", Cancelled: true},
		}
		cancelled := map[string]struct{}{}
		if err := addLegacyVSTPCancellations(context.Background(), realtime, time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC), cancelled); err != nil {
			mt.Fatal(err)
		}
		if _, ok := cancelled["old"]; !ok || len(cancelled) != 1 {
			mt.Fatalf("unexpected cancellations: %#v", cancelled)
		}
	})
}

func TestBoardPreservesUnexpiredOldVSTPCancellation(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousRedis := redis_client.Client
	redis_client.Client = client
	t.Cleanup(func() { redis_client.Client = previousRedis; client.Close() })
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("old cancellation without day alert", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		mt.AddMockResponses(
			mtest.CreateCursorResponse(0, mt.DB.Name()+".service_alerts", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".service_alerts", mtest.FirstBatch),
			mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch),
		)
		now := time.Now().UTC()
		serviceDate := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		start := serviceDate.AddDate(0, 0, -1)
		end := serviceDate.AddDate(0, 0, 1)
		journey := &ctdf.Journey{PrimaryIdentifier: "train", Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityMatchAll}}}, Path: []*ctdf.JourneyPathItem{{OriginStopRef: "stop", OriginDepartureTime: time.Date(0, 1, 1, 9, 0, 0, 0, time.UTC)}}}
		update := &ctdf.RealtimeJourney{PrimaryIdentifier: fmt.Sprintf("gb-nationalrail-vstp-cancellation-%s:%s:train", start.Format(ctdf.YearMonthDayFormat), end.Format(ctdf.YearMonthDayFormat)), Journey: journey, JourneyRunDate: start, Cancelled: true, ModificationDateTime: now.Add(-5 * time.Hour), TimeoutDurationMinutes: 24 * 60}
		if err := realtimestore.SaveRealtimeJourney(context.Background(), update); err != nil {
			mt.Fatal(err)
		}
		lookup := (Source{}).realtimeLookup([]*ctdf.Journey{journey}, serviceDate, []string{"stop"})
		entries := ctdf.GenerateBoardFromJourneysForServiceDate([]*ctdf.Journey{journey}, []string{"stop"}, serviceDate, serviceDate, false, lookup, ctdf.BoardTypeDeparture)
		if len(entries) != 1 || entries[0].Type != ctdf.DepartureBoardRecordTypeCancelled {
			mt.Fatalf("expected cancelled entry, got %#v", entries)
		}
	})
}

func TestDepartureBoardCachePreservesBlockEstimationMetadata(t *testing.T) {
	journeys := []*ctdf.Journey{{
		PrimaryIdentifier:       "journey",
		OtherIdentifiers:        map[string]string{"BlockNumber": "block-1"},
		DataSource:              &ctdf.DataSourceReference{DatasetID: "dataset-1"},
		DetailedRailInformation: &ctdf.JourneyDetailedRail{ReplacementBus: true},
		Path:                    []*ctdf.JourneyPathItem{{DestinationActivity: []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup}}},
	}}

	reduced, err := sheriff.Marshal(&sheriff.Options{Groups: []string{"departureboard-cache"}}, journeys)
	if err != nil {
		t.Fatalf("reduce cache value: %s", err)
	}
	encoded, err := json.Marshal(reduced)
	if err != nil {
		t.Fatalf("encode cache value: %s", err)
	}
	var decoded []*ctdf.Journey
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("decode cache value: %s", err)
	}

	if len(decoded) != 1 || decoded[0].OtherIdentifiers["BlockNumber"] != "block-1" {
		t.Fatalf("block metadata was not preserved: %#v", decoded)
	}
	if decoded[0].DataSource == nil || decoded[0].DataSource.DatasetID != "dataset-1" {
		t.Fatalf("dataset metadata was not preserved: %#v", decoded[0].DataSource)
	}
	if decoded[0].DetailedRailInformation == nil || !decoded[0].DetailedRailInformation.ReplacementBus || len(decoded[0].Path) != 1 || !reflect.DeepEqual(decoded[0].Path[0].DestinationActivity, journeys[0].Path[0].DestinationActivity) {
		t.Fatalf("board eligibility metadata was not preserved: %#v", decoded[0])
	}
}

type localGraphLoader struct {
	journey *ctdf.Journey
	loads   int
}

type overnightGraphLoader struct{ journeys []*ctdf.Journey }

func (l overnightGraphLoader) LoadStopJourneys(_ context.Context, _ []string, _ time.Time) ([]*ctdf.Journey, error) {
	return l.journeys, nil
}

func (l overnightGraphLoader) ScanJourneys(_ context.Context, _ []time.Time, _ string, visit func(*ctdf.Journey, string) error) error {
	for _, journey := range l.journeys {
		if err := visit(journey, journey.PrimaryIdentifier); err != nil {
			return err
		}
	}
	return nil
}

func TestPreviousServiceDayGraphWindowKeepsOvernightCalls(t *testing.T) {
	serviceDate := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	clock := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
	makeJourney := func(id string, at time.Time) *ctdf.Journey {
		return &ctdf.Journey{PrimaryIdentifier: id, DepartureTime: at, Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityMatchAll}}}, Path: []*ctdf.JourneyPathItem{{OriginStopRef: "stop", DestinationStopRef: "next", OriginDepartureTime: at, DestinationArrivalTime: at.Add(time.Minute)}}}
	}
	journeys := make([]*ctdf.Journey, 0, 141)
	for i := 0; i < 140; i++ {
		journeys = append(journeys, makeJourney(fmt.Sprintf("day-%03d", i), clock.Add(10*time.Hour+time.Duration(i)*time.Minute)))
	}
	journeys = append(journeys, makeJourney("overnight", clock.Add(25*time.Hour+30*time.Minute)))
	source := Source{DepartureGraph: departuregraph.New(overnightGraphLoader{journeys}, departuregraph.Config{Enabled: true})}
	q := query.DepartureBoard{Stop: &ctdf.Stop{PrimaryIdentifier: "stop"}, Count: 1, StartDateTime: serviceDate.Add(25 * time.Hour)}
	for attempt := 0; attempt < 2; attempt++ {
		got := source.getBoardJourneys(q, "unused", nil, serviceDate, ctdf.BoardTypeDeparture)
		if len(got) != 1 || got[0].PrimaryIdentifier != "overnight" {
			t.Fatalf("attempt %d: got %#v, want the overnight journey", attempt, got)
		}
	}
}

func TestSaturatedGraphFallsBackToCompleteJourneyCache(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousRedis := redis_client.Client
	redis_client.Client = client
	t.Cleanup(func() { redis_client.Client = previousRedis; client.Close() })
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("candidate limit", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		serviceDate := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
		clock := time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)
		journeys := make([]*ctdf.Journey, 0, 141)
		documents := make([]bson.D, 0, 141)
		for i := 0; i < 141; i++ {
			at := clock.Add(21*time.Hour + time.Duration(i)*time.Minute)
			if i == 140 {
				at = clock.Add(25*time.Hour + 30*time.Minute)
			}
			journey := &ctdf.Journey{PrimaryIdentifier: fmt.Sprintf("journey-%03d", i), DepartureTime: at, Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{Type: ctdf.AvailabilityMatchAll}}}, Path: []*ctdf.JourneyPathItem{{OriginStopRef: "stop", DestinationStopRef: "next", OriginDepartureTime: at, DestinationArrivalTime: at.Add(time.Minute)}}}
			encoded, err := bson.Marshal(journey)
			if err != nil {
				mt.Fatal(err)
			}
			var doc bson.D
			if err := bson.Unmarshal(encoded, &doc); err != nil {
				mt.Fatal(err)
			}
			journeys = append(journeys, journey)
			documents = append(documents, doc)
		}
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".journeys", mtest.FirstBatch, documents...))
		cache := &cachedresults.Cache{}
		cache.Setup()
		source := Source{DepartureGraph: departuregraph.New(overnightGraphLoader{journeys}, departuregraph.Config{Enabled: true}), CachedResults: cache}
		q := query.DepartureBoard{Stop: &ctdf.Stop{PrimaryIdentifier: "stop"}, Count: 1, StartDateTime: serviceDate.Add(25 * time.Hour)}
		got, fromGraph, err := source.getBoardJourneysWithSource(q, "test", bson.M{}, serviceDate, ctdf.BoardTypeDeparture)
		if err != nil || fromGraph || len(got) != len(journeys) {
			mt.Fatalf("got %d journeys, graph=%t, err=%v; want complete fallback", len(got), fromGraph, err)
		}
		if got[len(got)-1].PrimaryIdentifier != "journey-140" {
			mt.Fatal("fallback omitted the overnight journey")
		}
	})
}

func (l *localGraphLoader) LoadStopJourneys(_ context.Context, _ []string, _ time.Time) ([]*ctdf.Journey, error) {
	l.loads++
	return []*ctdf.Journey{l.journey}, nil
}

func (l *localGraphLoader) ScanJourneys(_ context.Context, _ []time.Time, _ string, visit func(*ctdf.Journey, string) error) error {
	return visit(l.journey, l.journey.PrimaryIdentifier)
}

func TestDepartureBoardCandidateLookupUsesLazyGraph(t *testing.T) {
	serviceTime := time.Date(0, time.January, 1, 8, 30, 0, 0, time.UTC)
	journey := &ctdf.Journey{
		PrimaryIdentifier: "journey-graph",
		DepartureTime:     serviceTime,
		Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{
			Type: ctdf.AvailabilityMatchAll,
		}}},
		Path: []*ctdf.JourneyPathItem{{
			OriginStopRef:          "stop-a",
			DestinationStopRef:     "stop-b",
			OriginDepartureTime:    serviceTime,
			DestinationArrivalTime: serviceTime.Add(10 * time.Minute),
		}},
	}
	loader := &localGraphLoader{journey: journey}
	graph := departuregraph.New(loader, departuregraph.Config{Enabled: true})
	source := Source{DepartureGraph: graph}
	stop := &ctdf.Stop{PrimaryIdentifier: "stop-a"}
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)

	journeys := source.getBoardJourneys(query.DepartureBoard{Stop: stop}, "unused", nil, serviceDate, ctdf.BoardTypeDeparture)
	if len(journeys) != 1 || journeys[0].PrimaryIdentifier != "journey-graph" {
		t.Fatalf("graph journeys = %#v", journeys)
	}
	if loader.loads != 1 {
		t.Fatalf("graph loads = %d, want 1", loader.loads)
	}

	journeys = source.getBoardJourneys(query.DepartureBoard{Stop: stop}, "unused", nil, serviceDate, ctdf.BoardTypeDeparture)
	if len(journeys) != 1 || loader.loads != 1 {
		t.Fatalf("completed graph lookup journeys=%d loads=%d, want 1 and 1", len(journeys), loader.loads)
	}
}

func TestDepartureGraphCandidateLimitOverfetchesAndBoundsResults(t *testing.T) {
	for _, test := range []struct {
		count int
		want  int
	}{
		{count: 0, want: 128},
		{count: 12, want: 128},
		{count: 24, want: 192},
		{count: 3000, want: 20000},
	} {
		if got := departureGraphCandidateLimit(test.count); got != test.want {
			t.Errorf("candidate limit for count %d = %d, want %d", test.count, got, test.want)
		}
	}
}

func TestBoardStopAliasFallbackIncludesAllBoardIdentifiers(t *testing.T) {
	aliases, requested := boardStopAliasFallback([]string{"station", "platform", "station"})

	want := []string{"station", "platform"}
	if !reflect.DeepEqual(aliases["station"], want) || !reflect.DeepEqual(aliases["platform"], want) {
		t.Fatalf("fallback aliases = %#v, want both board identifiers: %v", aliases, want)
	}
	if len(requested) != 2 {
		t.Fatalf("requested identifiers = %#v, want two unique identifiers", requested)
	}
}
