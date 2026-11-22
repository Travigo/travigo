package tfl

import (
	"reflect"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func TestTfLRealtimeRespectsSelectedWindow(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	start := now.AddDate(0, 0, -3)
	end := start.Add(48 * time.Hour)
	if tflRealtimeWindowOverlaps(start, end, now) || tflRealtimeTimeInWindow(now.Add(time.Hour), start, end, now) {
		t.Fatal("current predictions must not appear on a historical board")
	}
	start, end = now.Add(-time.Hour), now.Add(time.Hour)
	if !tflRealtimeWindowOverlaps(start, end, now) || !tflRealtimeTimeInWindow(now, start, end, now) || tflRealtimeTimeInWindow(end, start, end, now) {
		t.Fatal("realtime prediction window or exclusive end is incorrect")
	}
}

func TestTfLCancellationLookupGroupsByOperatingDate(t *testing.T) {
	date := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	journeys := []ctdf.RealtimeJourney{
		{Journey: &ctdf.Journey{PrimaryIdentifier: "first"}, JourneyRunDate: date},
		{Journey: &ctdf.Journey{PrimaryIdentifier: "second"}, JourneyRunDate: date.AddDate(0, 0, 1)},
		{Journey: &ctdf.Journey{PrimaryIdentifier: "unknown"}},
	}
	want := map[string][]string{"2026-11-03": {"first"}, "2026-11-04": {"second"}}
	if got := tflJourneyIDsByServiceDate(journeys); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestTfLBackfillUsesTimetableDirectionWithSparseLivePaths(t *testing.T) {
	base := time.Date(2026, 11, 22, 12, 14, 0, 0, time.UTC)
	stopIDs := map[string]struct{}{"station": {}, "station-platform": {}}
	service := &ctdf.Service{
		PrimaryIdentifier: "jubilee", OperatorRef: tflOperatorRef,
		OtherIdentifiers: []string{"jubilee-timetable"},
	}
	services := loadTFLBoardServices([]*ctdf.Service{service}, nil)
	westbound := &ctdf.DepartureBoard{
		Time: base.Add(5 * time.Minute), Type: ctdf.DepartureBoardRecordTypeRealtimeTracked,
		Journey: &ctdf.Journey{ServiceRef: "jubilee"},
	}
	eastbound := &ctdf.DepartureBoard{
		Time: base.Add(time.Minute), Type: ctdf.DepartureBoardRecordTypeRealtimeTracked,
		Journey: &ctdf.Journey{ServiceRef: "jubilee"},
	}
	cancelled := &ctdf.DepartureBoard{Time: base.Add(10 * time.Minute), Type: ctdf.DepartureBoardRecordTypeCancelled}
	livePaths := map[*ctdf.DepartureBoard]boardDirectionPath{
		westbound: {before: []string{"east"}, after: []string{"west"}},
		eastbound: {before: []string{"west"}, after: []string{"east"}},
	}
	keep := tflBackfillFilter(
		[]*ctdf.DepartureBoard{eastbound, westbound, cancelled}, livePaths, stopIDs, ctdf.BoardTypeDeparture,
		services, map[string]string{"west-platform": "west", "east-platform": "east"},
	)
	for _, tc := range []struct {
		name    string
		service string
		minutes int
		calls   []string
		keep    bool
	}{
		{"westbound before live", "jubilee-timetable", 2, []string{"east", "station-platform", "near-west", "west-platform"}, false},
		{"westbound at live time", "jubilee-timetable", 5, []string{"east", "station-platform", "near-west", "west-platform"}, false},
		{"westbound after live", "jubilee-timetable", 6, []string{"east", "station-platform", "near-west", "west-platform"}, true},
		{"eastbound before live", "jubilee-timetable", 0, []string{"west", "station", "near-east", "east-platform"}, false},
		{"eastbound after its own live time", "jubilee-timetable", 2, []string{"west", "station", "near-east", "east-platform"}, true},
		{"shared approach from east, different branch", "jubilee-timetable", 2, []string{"east", "station", "branch"}, true},
		{"another line", "bakerloo", 2, []string{"east", "station", "west"}, true},
		{"insufficient direction evidence", "jubilee-timetable", 2, []string{"station", "other"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entry := &ctdf.DepartureBoard{
				Time:    base.Add(time.Duration(tc.minutes) * time.Minute),
				Type:    ctdf.DepartureBoardRecordTypeScheduled,
				Journey: &ctdf.Journey{ServiceRef: tc.service, Calls: directionTestCalls(tc.calls...)},
			}
			if got := keep(entry); got != tc.keep {
				t.Errorf("keep = %t, want %t", got, tc.keep)
			}
		})
	}
	if !keep(cancelled) || !keep(westbound) {
		t.Fatal("the timetable filter should retain cancellations and live departures")
	}
	if filter := tflBackfillFilter([]*ctdf.DepartureBoard{cancelled}, nil, stopIDs, ctdf.BoardTypeDeparture, services, nil); filter != nil {
		t.Fatal("a board with no live predictions should use the full timetable")
	}
}

func TestTfLArrivalBackfillMatchesApproachAndRepeatedStop(t *testing.T) {
	now := time.Date(2026, 11, 22, 12, 14, 0, 0, time.UTC)
	stopIDs := map[string]struct{}{"station": {}}
	calls := directionTestCalls("west", "station", "loop", "east", "station")
	journey := &ctdf.Journey{PrimaryIdentifier: "loop train", ServiceRef: "line", Calls: calls}
	eastbound := &ctdf.DepartureBoard{
		Time: now.Add(time.Minute), Type: ctdf.DepartureBoardRecordTypeRealtimeTracked, Journey: journey,
	}
	paths := map[*ctdf.DepartureBoard]boardDirectionPath{eastbound: {before: []string{"east"}}}
	keep := tflBackfillFilter([]*ctdf.DepartureBoard{eastbound}, paths, stopIDs, ctdf.BoardTypeArrival, nil, nil)
	entry := &ctdf.DepartureBoard{
		Time: now, Type: ctdf.DepartureBoardRecordTypeScheduled,
		ServiceDate: now.Format(ctdf.YearMonthDayFormat), Journey: journey,
	}
	entry.OccurrenceIdentifier = ctdf.BoardOccurrenceIdentifier(journey.PrimaryIdentifier, entry.ServiceDate, 1, ctdf.BoardTypeArrival)
	if !keep(entry) {
		t.Error("the first visit approaches from the west and should retain its timetable")
	}
	entry.OccurrenceIdentifier = ctdf.BoardOccurrenceIdentifier(journey.PrimaryIdentifier, entry.ServiceDate, 4, ctdf.BoardTypeArrival)
	if keep(entry) {
		t.Error("the second visit approaches from the east and should use the live cutoff")
	}
}

func TestTfLLiveDirectionUsesObservedOccurrenceWhenRouteRepeatsStop(t *testing.T) {
	calls := directionTestCalls("prefix", "station", "east", "loop", "station", "west")
	prediction := &ctdf.RealtimeJourneyStops{StopRef: "station", JourneyStopIndex: 2}
	journey := &ctdf.RealtimeJourney{
		Journey: &ctdf.Journey{Calls: calls},
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"loop":    {StopRef: "loop", JourneyStopIndex: 1},
			"station": prediction,
			"west":    {StopRef: "west", JourneyStopIndex: 3},
		},
	}
	got := liveBoardDirectionPath(journey, prediction, map[string]struct{}{"station": {}})
	want := boardDirectionPath{before: []string{"loop"}, after: []string{"west"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("direction path = %#v, want %#v", got, want)
	}
}

func TestTfLDirectionStopAliasesPreferParentStation(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("parent and platform records", func(mt *mtest.T) {
		previousDB := database.Instance
		database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
		mt.Cleanup(func() { database.Instance = previousDB })
		mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+".stops", mtest.FirstBatch,
			bson.D{{Key: "primaryidentifier", Value: "west"}},
			bson.D{{Key: "primaryidentifier", Value: "east"}, {Key: "platforms", Value: bson.A{
				bson.M{"primaryidentifier": "east-platform"},
			}}},
			bson.D{{Key: "primaryidentifier", Value: "east-platform"}},
		))
		paths := map[*ctdf.DepartureBoard]boardDirectionPath{
			{}: {before: []string{"west"}, after: []string{"east-platform"}},
		}
		want := map[string]string{"west": "west", "east": "east", "east-platform": "east"}
		if got := loadDirectionStopAliases(paths); !reflect.DeepEqual(got, want) {
			mt.Fatalf("aliases = %#v, want %#v", got, want)
		}
	})
}

func directionTestCalls(refs ...string) []ctdf.JourneyCall {
	calls := make([]ctdf.JourneyCall, len(refs))
	for i, ref := range refs {
		calls[i] = ctdf.JourneyCall{StopRef: ref, Start: i == 0, End: i == len(refs)-1}
	}
	return calls
}
