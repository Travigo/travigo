package departuregraph

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type Server struct {
	graph    Provider
	stats    func() Stats
	requests *requestTracker
}

func NewServer(graph *Graph) *Server {
	return &Server{graph: graph, stats: graph.Stats, requests: newRequestTracker()}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /v1/stats", s.handleStats)
	mux.HandleFunc("POST "+departuresPath, s.handleDepartures)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeGraphJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	writeGraphJSON(w, http.StatusOK, ServiceStats{
		Stats:       s.stats(),
		GeneratedAt: now,
		Requests:    s.requests.stats(now),
		Memory:      currentMemoryStats(),
	})
}

func (s *Server) handleDepartures(w http.ResponseWriter, r *http.Request) {
	started := s.requests.begin()
	failed := true
	defer func() { s.requests.finish(started, failed) }()

	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024*1024))
	decoder.DisallowUnknownFields()
	var request departuresRequest
	if err := decoder.Decode(&request); err != nil {
		writeGraphError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if len(request.StopRefs) == 0 || len(request.StopRefs) > 256 {
		writeGraphError(w, http.StatusBadRequest, "stopRefs must contain between 1 and 256 identifiers")
		return
	}
	if len(request.DestinationRefs) > 256 {
		writeGraphError(w, http.StatusBadRequest, "destinationRefs must contain no more than 256 identifiers")
		return
	}
	serviceDate, err := time.Parse(ctdf.YearMonthDayFormat, request.ServiceDate)
	if err != nil {
		writeGraphError(w, http.StatusBadRequest, "serviceDate must use YYYY-MM-DD")
		return
	}
	if request.Limit < 0 || request.Limit > 20000 {
		writeGraphError(w, http.StatusBadRequest, "limit must be between 0 and 20000")
		return
	}
	var notBefore time.Time
	if request.NotBefore != "" {
		notBefore, err = time.Parse(time.RFC3339Nano, request.NotBefore)
		if err != nil {
			writeGraphError(w, http.StatusBadRequest, "notBefore must use RFC3339")
			return
		}
	}
	primary := request.PrimaryStopRef
	if primary == "" {
		primary = request.StopRefs[0]
	}
	otherRefs := make([]string, 0, len(request.StopRefs))
	for _, stopRef := range request.StopRefs {
		if stopRef != "" && stopRef != primary {
			otherRefs = append(otherRefs, stopRef)
		}
	}

	stop := &ctdf.Stop{
		PrimaryIdentifier: primary,
		OtherIdentifiers:  otherRefs,
	}
	var journeys []*ctdf.Journey
	if len(request.DestinationRefs) > 0 {
		destinationPrimary := request.DestinationStopRef
		if destinationPrimary == "" {
			destinationPrimary = request.DestinationRefs[0]
		}
		destinationOtherRefs := make([]string, 0, len(request.DestinationRefs))
		for _, stopRef := range request.DestinationRefs {
			if stopRef != "" && stopRef != destinationPrimary {
				destinationOtherRefs = append(destinationOtherRefs, stopRef)
			}
		}
		journeys, err = s.graph.JourneysTowardsStopWindow(r.Context(), stop, &ctdf.Stop{
			PrimaryIdentifier: destinationPrimary,
			OtherIdentifiers:  destinationOtherRefs,
		}, serviceDate, notBefore, request.Limit)
	} else {
		journeys, err = s.graph.JourneysForStopWindow(r.Context(), stop, serviceDate, notBefore, request.Limit)
	}
	if err != nil {
		writeGraphError(w, http.StatusServiceUnavailable, fmt.Sprintf("load departures: %v", err))
		return
	}
	failed = false
	writeGraphJSON(w, http.StatusOK, departuresResponse{Journeys: journeys})
}

func writeGraphError(w http.ResponseWriter, status int, message string) {
	writeGraphJSON(w, status, map[string]string{"error": message})
}

func writeGraphJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
