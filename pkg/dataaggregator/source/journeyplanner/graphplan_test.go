package journeyplanner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/departuregraph"
)

type graphRequestTransport struct {
	request departuregraph.PlanRequest
}

type arrivalByTransport struct {
	latestDeparture time.Time
}

func (transport arrivalByTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	var planRequest departuregraph.PlanRequest
	if err := json.NewDecoder(request.Body).Decode(&planRequest); err != nil {
		return nil, err
	}
	departure := transport.latestDeparture
	arrival := departure.Add(30 * time.Minute)
	if planRequest.StartDateTime.After(transport.latestDeparture) {
		departure = planRequest.StartDateTime
		arrival = departure.Add(2 * time.Hour)
	}
	response, err := json.Marshal(departuregraph.PlanResponse{Plans: []departuregraph.Plan{{StartTime: departure, ArrivalTime: arrival}}})
	if err != nil {
		return nil, err
	}
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(response))), Request: request}, nil
}

func (transport *graphRequestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := json.NewDecoder(request.Body).Decode(&transport.request); err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"plans":[]}`)),
		Request:    request,
	}, nil
}

func TestJourneyPlanRequestsOnlyTheRequiredGraphCandidates(t *testing.T) {
	transport := &graphRequestTransport{}
	client := departuregraph.NewClient("http://journey-graph", &http.Client{Transport: transport})

	_, err := (Source{JourneyGraph: client}).JourneyPlanQuery(query.JourneyPlan{
		OriginStop:      &ctdf.Stop{PrimaryIdentifier: "origin"},
		DestinationStop: &ctdf.Stop{PrimaryIdentifier: "destination"},
		StartDateTime:   time.Date(2026, time.August, 15, 10, 0, 0, 0, time.UTC),
		Count:           5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if transport.request.Count != 5 {
		t.Fatalf("graph candidate count = %d, want requested count 5", transport.request.Count)
	}
}

func TestJourneyPlanForwardsRealtimeRecoveryExclusions(t *testing.T) {
	transport := &graphRequestTransport{}
	client := departuregraph.NewClient("http://journey-graph", &http.Client{Transport: transport})

	_, err := (Source{JourneyGraph: client}).JourneyPlanQuery(query.JourneyPlan{
		OriginStop: &ctdf.Stop{PrimaryIdentifier: "origin"}, DestinationStop: &ctdf.Stop{PrimaryIdentifier: "destination"},
		StartDateTime: time.Date(2026, time.August, 15, 10, 0, 0, 0, time.UTC), Count: 1,
		ExcludedJourneyRefs: []string{"cancelled-journey"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(transport.request.ExcludedJourneyRefs) != 1 || transport.request.ExcludedJourneyRefs[0] != "cancelled-journey" {
		t.Fatalf("excluded journeys = %#v", transport.request.ExcludedJourneyRefs)
	}
}

func TestPlanArrivingByChoosesLatestDepartureThatMeetsDeadline(t *testing.T) {
	deadline := time.Date(2026, time.August, 18, 9, 0, 0, 0, time.UTC)
	latestDeparture := deadline.Add(-30 * time.Minute)
	client := departuregraph.NewClient("http://journey-graph", &http.Client{Transport: arrivalByTransport{latestDeparture: latestDeparture}})

	result, err := (Source{JourneyGraph: client}).planArrivingBy(context.Background(), departuregraph.PlanRequest{
		OriginRefs: []string{"origin"}, DestinationRefs: []string{"destination"}, StartDateTime: deadline.Add(-12 * time.Hour), Count: 1,
	}, deadline)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Response.Plans) != 1 {
		t.Fatalf("arrival-by plans = %#v", result.Response.Plans)
	}
	if got := result.Response.Plans[0].StartTime; !got.Equal(latestDeparture) {
		t.Fatalf("departure = %s, want %s", got, latestDeparture)
	}
}

func TestApplyRealtimeLegTimesUsesOccurrenceAndRejectsCancelledCall(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{OriginStopRef: "origin", DestinationStopRef: "destination"}}}
	journey.RealtimeJourney = &ctdf.RealtimeJourney{Journey: journey, Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{
		StopRef:          "origin",
		JourneyStopIndex: 0,
		DepartureTime:    time.Date(0, time.January, 1, 10, 5, 0, 0, time.UTC),
	})
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{
		StopRef:          "destination",
		JourneyStopIndex: 1,
		ArrivalTime:      time.Date(0, time.January, 1, 10, 45, 0, 0, time.UTC),
	})
	item := &ctdf.JourneyPlanRouteItem{
		Journey: journey, OriginStopRef: "origin", DestinationStopRef: "destination",
		StartTime: serviceDate.Add(10 * time.Hour), ArrivalTime: serviceDate.Add(40 * time.Minute),
	}
	if !applyRealtimeLegTimes(item, 0, 1) {
		t.Fatal("valid realtime calls were rejected")
	}
	if got := item.StartTime; !got.Equal(serviceDate.Add(10*time.Hour + 5*time.Minute)) {
		t.Fatalf("start time = %v", got)
	}
	if got := item.ArrivalTime; !got.Equal(serviceDate.Add(10*time.Hour + 45*time.Minute)) {
		t.Fatalf("arrival time = %v", got)
	}

	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "origin", JourneyStopIndex: 0, Cancelled: true})
	if applyRealtimeLegTimes(item, 0, 1) {
		t.Fatal("cancelled boarding call was accepted")
	}
}

func TestApplyRealtimeLegTimesUsesGraphOccurrenceForRepeatedStop(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	journey := &ctdf.Journey{Path: []*ctdf.JourneyPathItem{
		{OriginStopRef: "loop", DestinationStopRef: "middle"},
		{OriginStopRef: "middle", DestinationStopRef: "loop"},
		{OriginStopRef: "loop", DestinationStopRef: "destination"},
	}}
	journey.RealtimeJourney = &ctdf.RealtimeJourney{Journey: journey, Stops: map[string]*ctdf.RealtimeJourneyStops{}}
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "loop", JourneyStopIndex: 0, Cancelled: true})
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "loop", JourneyStopIndex: 2, DepartureTime: serviceDate.Add(11 * time.Hour)})
	journey.RealtimeJourney.SetRealtimeStop(&ctdf.RealtimeJourneyStops{StopRef: "destination", JourneyStopIndex: 3, ArrivalTime: serviceDate.Add(12 * time.Hour)})
	item := &ctdf.JourneyPlanRouteItem{
		Journey: journey, OriginStopRef: "loop", DestinationStopRef: "destination",
		StartTime: serviceDate.Add(10 * time.Hour), ArrivalTime: serviceDate.Add(12 * time.Hour),
	}
	if !applyRealtimeLegTimes(item, 2, 3) {
		t.Fatal("later occurrence was confused with the cancelled first call")
	}
	if !item.StartTime.Equal(serviceDate.Add(11 * time.Hour)) {
		t.Fatalf("start time = %v", item.StartTime)
	}
}
