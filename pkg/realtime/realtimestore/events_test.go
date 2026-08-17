package realtimestore

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestRealtimeJourneyEventsDetectsNewCancellation(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Cancelled:         false,
	}
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Cancelled:         true,
	}

	events := realtimeJourneyEvents(previous, current, true, now)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != ctdf.EventTypeRealtimeJourneyCancelled {
		t.Fatalf("expected cancellation event, got %s", events[0].Type)
	}
	if !events[0].Timestamp.Equal(now) {
		t.Fatalf("expected timestamp %s, got %s", now, events[0].Timestamp)
	}

	body, ok := events[0].Body.(ctdf.RealtimeJourney)
	if !ok {
		t.Fatalf("expected realtime journey body, got %T", events[0].Body)
	}
	if body.PrimaryIdentifier != current.PrimaryIdentifier {
		t.Fatalf("expected body id %s, got %s", current.PrimaryIdentifier, body.PrimaryIdentifier)
	}
}

func TestRealtimeJourneyEventsDetectsNextStopChanged(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		NextStopRef:       "stop-a",
		NextStopIndex:     1,
	}
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		NextStopRef:       "stop-b",
		NextStopIndex:     2,
	}

	events := realtimeJourneyEvents(previous, current, true, now)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ctdf.EventTypeRealtimeJourneyNextStopChanged {
		t.Fatalf("expected next-stop-changed event, got %s", events[0].Type)
	}
	if !events[0].Timestamp.Equal(now) {
		t.Fatalf("expected timestamp %s, got %s", now, events[0].Timestamp)
	}
	body, ok := events[0].Body.(ctdf.RealtimeJourney)
	if !ok || body.NextStopRef != current.NextStopRef || body.NextStopIndex != current.NextStopIndex {
		t.Fatalf("unexpected next-stop event body: %#v", events[0].Body)
	}
}

func TestRealtimeJourneyEventsDetectsCreatedJourney(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Cancelled:         true,
	}

	events := realtimeJourneyEvents(nil, current, true, now)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}

	if events[0].Type != ctdf.EventTypeRealtimeJourneyCreated {
		t.Fatalf("expected created event, got %s", events[0].Type)
	}
	if !events[0].Timestamp.Equal(now) {
		t.Fatalf("expected timestamp %s, got %s", now, events[0].Timestamp)
	}

	body, ok := events[0].Body.(ctdf.RealtimeJourney)
	if !ok {
		t.Fatalf("expected realtime journey body, got %T", events[0].Body)
	}
	if body.PrimaryIdentifier != current.PrimaryIdentifier {
		t.Fatalf("expected body id %s, got %s", current.PrimaryIdentifier, body.PrimaryIdentifier)
	}
}

func TestRealtimeJourneyEventsDetectsActivelyTracked(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		ActivelyTracked:   false,
	}
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		ActivelyTracked:   true,
	}

	events := realtimeJourneyEvents(previous, current, true, now)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ctdf.EventTypeRealtimeJourneyActivelyTracked {
		t.Fatalf("expected actively-tracked event, got %s", events[0].Type)
	}
	if !events[0].Timestamp.Equal(now) {
		t.Fatalf("expected timestamp %s, got %s", now, events[0].Timestamp)
	}
}

func TestRealtimeJourneyEventsDetectsLocationTextChanged(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier:          "realtime-test",
		VehicleLocationDescription: "Approaching Cambridge",
	}
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier:          "realtime-test",
		VehicleLocationDescription: "At Cambridge",
	}

	events := realtimeJourneyEvents(previous, current, true, now)
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != ctdf.EventTypeRealtimeJourneyLocationTextChanged {
		t.Fatalf("expected location-text-changed event, got %s", events[0].Type)
	}
	if !events[0].Timestamp.Equal(now) {
		t.Fatalf("expected timestamp %s, got %s", now, events[0].Timestamp)
	}
	body, ok := events[0].Body.(ctdf.RealtimeJourney)
	if !ok || body.VehicleLocationDescription != current.VehicleLocationDescription {
		t.Fatalf("unexpected location-text event body: %#v", events[0].Body)
	}
}

func TestRealtimeJourneyEventsDetectsOverlayCreated(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "realtime-test",
		SuppressFromDepartures: true,
		ReplacedByJourneyRef:   "overlay-journey",
	}

	events := realtimeJourneyEvents(nil, current, true, now)
	if len(events) != 2 {
		t.Fatalf("expected created and overlay events, got %d", len(events))
	}
	if events[1].Type != ctdf.EventTypeRealtimeJourneyOverlayCreated {
		t.Fatalf("expected overlay-created event, got %s", events[1].Type)
	}
}

func TestRealtimeJourneyEventsDoesNotRepeatExistingOverlay(t *testing.T) {
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "realtime-test",
		SuppressFromDepartures: true,
		ReplacedByJourneyRef:   "overlay-journey",
	}
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "realtime-test",
		SuppressFromDepartures: true,
		ReplacedByJourneyRef:   "overlay-journey",
	}

	if events := realtimeJourneyEvents(previous, current, true, time.Now()); len(events) != 0 {
		t.Fatalf("expected no repeated overlay-created event, got %#v", events)
	}
}

func TestRealtimeJourneyEventsIgnoresUnknownPreviousState(t *testing.T) {
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Cancelled:         true,
	}

	events := realtimeJourneyEvents(nil, current, false, time.Now())
	if len(events) != 0 {
		t.Fatalf("expected no events, got %d", len(events))
	}
}

func TestRealtimeJourneyEventsDetectsPlatformSetAndChanged(t *testing.T) {
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"stop-a": {
				StopRef:  "stop-a",
				Platform: "",
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			},
			"stop-b": {
				StopRef:  "stop-b",
				Platform: "1",
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			},
			"stop-c": {
				StopRef:  "stop-c",
				Platform: "2",
				TimeType: ctdf.RealtimeJourneyStopTimeHistorical,
			},
		},
	}
	current := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"stop-a": {
				StopRef:  "stop-a",
				Platform: "4",
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			},
			"stop-b": {
				StopRef:  "stop-b",
				Platform: "3",
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			},
			"stop-c": {
				StopRef:  "stop-c",
				Platform: "5",
				TimeType: ctdf.RealtimeJourneyStopTimeHistorical,
			},
			"stop-d": {
				StopRef:  "stop-d",
				Platform: "6",
				TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture,
			},
		},
	}

	events := realtimeJourneyEvents(previous, current, true, time.Now())
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}

	eventsByType := map[ctdf.EventType]ctdf.Event{}
	for _, event := range events {
		eventsByType[event.Type] = event
	}

	platformSet, ok := eventsByType[ctdf.EventTypeRealtimeJourneyPlatformSet]
	if !ok {
		t.Fatal("expected platform set event")
	}
	assertPlatformEventBody(t, platformSet, "stop-a", 0, "", "4")

	platformChanged, ok := eventsByType[ctdf.EventTypeRealtimeJourneyPlatformChanged]
	if !ok {
		t.Fatal("expected platform changed event")
	}
	assertPlatformEventBody(t, platformChanged, "stop-b", 0, "1", "3")
}

func TestRealtimeJourneyEventsDistinguishRepeatedStopCalls(t *testing.T) {
	previous := &ctdf.RealtimeJourney{PrimaryIdentifier: "realtime-test", Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	current := &ctdf.RealtimeJourney{PrimaryIdentifier: "realtime-test", Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	for _, index := range []int{0, 3} {
		previous.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: index, Platform: "1", TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture})
		current.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: index, Platform: "2", TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture})
	}

	events := realtimeJourneyEvents(previous, current, true, time.Now())
	if len(events) != 2 {
		t.Fatalf("expected a platform event for both Waterloo calls, got %d", len(events))
	}
	seenIndexes := map[int]bool{}
	for _, event := range events {
		body := event.Body.(map[string]interface{})
		if body["Stop"] != "waterloo" {
			t.Fatalf("expected physical stop ref in event, got %v", body["Stop"])
		}
		seenIndexes[body["JourneyStopIndex"].(int)] = true
	}
	if !seenIndexes[0] || !seenIndexes[3] {
		t.Fatalf("expected occurrence indexes 0 and 3, got %v", seenIndexes)
	}
}

func TestRealtimeJourneyEventsCompareLegacyPreviousStop(t *testing.T) {
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "waterloo"}}}
	previous := &ctdf.RealtimeJourney{
		PrimaryIdentifier: "realtime-test",
		Journey:           journey,
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"waterloo": {StopRef: "waterloo", Platform: "1", TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture},
		},
	}
	current := &ctdf.RealtimeJourney{PrimaryIdentifier: "realtime-test", Journey: journey}
	current.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "waterloo", JourneyStopIndex: 1, Platform: "2", TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture})

	events := realtimeJourneyEvents(previous, current, true, time.Now())
	if len(events) != 1 {
		t.Fatalf("expected platform change across legacy migration, got %d events", len(events))
	}
	assertPlatformEventBody(t, events[0], "waterloo", 1, "1", "2")
}

func TestRealtimeJourneyEventsDetectsIncreasingDelay(t *testing.T) {
	runDate := time.Date(2026, time.August, 17, 0, 0, 0, 0, time.Local)
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "destination", OriginDepartureTime: time.Date(1, 1, 1, 8, 0, 0, 0, time.Local), DestinationArrivalTime: time.Date(1, 1, 1, 9, 0, 0, 0, time.Local)}}}
	previous := &ctdf.RealtimeJourney{PrimaryIdentifier: "realtime-test", Journey: journey, JourneyRunDate: runDate}
	previous.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "origin", JourneyStopIndex: 0, DepartureTime: runDate.Add(5*time.Minute + 8*time.Hour), TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture})
	current := &ctdf.RealtimeJourney{PrimaryIdentifier: "realtime-test", Journey: journey, JourneyRunDate: runDate}
	current.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "origin", JourneyStopIndex: 0, DepartureTime: runDate.Add(10*time.Minute + 8*time.Hour), TimeType: ctdf.RealtimeJourneyStopTimeEstimatedFuture})

	events := realtimeJourneyEvents(previous, current, true, time.Now())
	if len(events) != 1 || events[0].Type != ctdf.EventTypeRealtimeJourneyDelayed {
		t.Fatalf("delay events = %#v, want one delay event", events)
	}
}

func assertPlatformEventBody(t *testing.T, event ctdf.Event, stop string, journeyStopIndex int, oldPlatform string, newPlatform string) {
	t.Helper()

	body, ok := event.Body.(map[string]interface{})
	if !ok {
		t.Fatalf("expected map body, got %T", event.Body)
	}
	if body["Stop"] != stop {
		t.Fatalf("expected stop %s, got %v", stop, body["Stop"])
	}
	if body["JourneyStopIndex"] != journeyStopIndex {
		t.Fatalf("expected journey stop index %d, got %v", journeyStopIndex, body["JourneyStopIndex"])
	}
	if body["NewPlatform"] != newPlatform {
		t.Fatalf("expected new platform %s, got %v", newPlatform, body["NewPlatform"])
	}
	if oldPlatform != "" && body["OldPlatform"] != oldPlatform {
		t.Fatalf("expected old platform %s, got %v", oldPlatform, body["OldPlatform"])
	}
	if _, ok := body["RealtimeJourney"].(ctdf.RealtimeJourney); !ok {
		t.Fatalf("expected realtime journey body, got %T", body["RealtimeJourney"])
	}
}
