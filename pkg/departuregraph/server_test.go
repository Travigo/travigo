package departuregraph

import (
	"context"
	"net/http"
	"net/http/httptest"
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
