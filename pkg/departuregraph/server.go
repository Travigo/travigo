package departuregraph

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type Server struct {
	graph Provider
	stats func() Stats
}

func NewServer(graph *Graph) *Server {
	return &Server{graph: graph, stats: graph.Stats}
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
	writeGraphJSON(w, http.StatusOK, s.stats())
}

func (s *Server) handleDepartures(w http.ResponseWriter, r *http.Request) {
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
	serviceDate, err := time.Parse(ctdf.YearMonthDayFormat, request.ServiceDate)
	if err != nil {
		writeGraphError(w, http.StatusBadRequest, "serviceDate must use YYYY-MM-DD")
		return
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

	journeys, err := s.graph.JourneysForStop(r.Context(), &ctdf.Stop{
		PrimaryIdentifier: primary,
		OtherIdentifiers:  otherRefs,
	}, serviceDate)
	if err != nil {
		writeGraphError(w, http.StatusServiceUnavailable, fmt.Sprintf("load departures: %v", err))
		return
	}
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
