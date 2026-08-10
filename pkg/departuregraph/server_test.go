package departuregraph

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestClientQueriesGraphService(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	loader := &fakeLoader{journeys: []*ctdf.Journey{testJourney()}}
	graph := New(loader, Config{Enabled: true})
	httpClient := &http.Client{Transport: handlerTransport{handler: NewServer(graph).Handler()}}
	client := NewClient("http://departure-graph", httpClient)
	journeys, err := client.JourneysForStop(context.Background(), &ctdf.Stop{
		PrimaryIdentifier: "stop-a",
		OtherIdentifiers:  []string{"alias-a"},
	}, serviceDate)
	if err != nil {
		t.Fatalf("query graph service: %v", err)
	}
	if len(journeys) != 1 {
		t.Fatalf("journeys = %d, want 1", len(journeys))
	}
	assertMaterializedJourney(t, journeys[0])
	if loader.stopLoads != 1 {
		t.Fatalf("service stop loads = %d, want 1", loader.stopLoads)
	}
	journeys, err = client.JourneysForStopWindow(
		context.Background(),
		&ctdf.Stop{PrimaryIdentifier: "stop-a"},
		serviceDate,
		serviceDate.Add(25*time.Hour+6*time.Minute),
		1,
	)
	if err != nil {
		t.Fatalf("query graph service window: %v", err)
	}
	if len(journeys) != 0 {
		t.Fatalf("windowed journeys = %d, want 0", len(journeys))
	}
}

func TestStatsEndpointReportsRequestsLookupsAndMemory(t *testing.T) {
	serviceDate := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	loader := &fakeLoader{journeys: []*ctdf.Journey{testJourney()}}
	graph := New(loader, Config{Enabled: true})
	server := NewServer(graph)
	httpClient := &http.Client{Transport: handlerTransport{handler: server.Handler()}}
	client := NewClient("http://departure-graph", httpClient)
	stop := &ctdf.Stop{PrimaryIdentifier: "stop-a"}
	for index := 0; index < 2; index++ {
		if _, err := client.JourneysForStop(context.Background(), stop, serviceDate); err != nil {
			t.Fatalf("query graph service: %v", err)
		}
		if index == 0 {
			waitForStopComplete(t, graph, stop, serviceDate)
		}
	}
	invalidRequest, err := http.NewRequest(http.MethodPost, "http://departure-graph/v1/departures", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	invalidResponse, err := httpClient.Do(invalidRequest)
	if err != nil {
		t.Fatalf("query invalid departure request: %v", err)
	}
	invalidResponse.Body.Close()
	if invalidResponse.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid request status = %d, want 400", invalidResponse.StatusCode)
	}

	request, err := http.NewRequest(http.MethodGet, "http://departure-graph/v1/stats", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := httpClient.Do(request)
	if err != nil {
		t.Fatalf("query stats: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read stats: %v", err)
	}
	var stats ServiceStats
	if err := json.Unmarshal(body, &stats); err != nil {
		t.Fatalf("decode stats: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatalf("decode stats fields: %v", err)
	}
	for _, field := range []string{"Strings", "Journeys", "Paths", "DepartureBuckets", "CompleteStops", "CompleteDays"} {
		if _, exists := fields[field]; !exists {
			t.Errorf("legacy stats field %q is missing", field)
		}
	}
	if stats.Requests.Total != 3 || stats.Requests.Completed != 3 || stats.Requests.Failed != 1 || stats.Requests.CompletedLastMinute != 3 || stats.Requests.FailuresLastMinute != 1 {
		t.Fatalf("unexpected request stats: %#v", stats.Requests)
	}
	if stats.Requests.RequestsPerSecondLastMinute <= 0 || stats.Requests.AverageLatencyMillis < 0 {
		t.Fatalf("missing request performance stats: %#v", stats.Requests)
	}
	if stats.Lookups.Total != 2 || stats.Lookups.Hits != 1 || stats.Lookups.Misses != 1 || stats.Lookups.LazyFills != 1 {
		t.Fatalf("unexpected lookup stats: %#v", stats.Lookups)
	}
	if stats.Lookups.HitRate != 0.5 {
		t.Fatalf("hit rate = %f, want 0.5", stats.Lookups.HitRate)
	}
	if stats.Memory.HeapAllocBytes == 0 || stats.Memory.RuntimeSysBytes == 0 || stats.Memory.Goroutines == 0 {
		t.Fatalf("missing memory stats: %#v", stats.Memory)
	}
}

func TestClientReturnsServiceErrors(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "graph unavailable", http.StatusServiceUnavailable)
	})

	client := NewClient("http://departure-graph", &http.Client{Transport: handlerTransport{handler: handler}})
	_, err := client.JourneysForStop(context.Background(), &ctdf.Stop{PrimaryIdentifier: "stop-a"}, time.Now())
	if err == nil {
		t.Fatal("expected graph service error")
	}
}

type handlerTransport struct {
	handler http.Handler
}

func (transport handlerTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response := httptest.NewRecorder()
	transport.handler.ServeHTTP(response, request)
	return response.Result(), nil
}
