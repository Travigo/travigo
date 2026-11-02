package vehicletracker

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
)

func TestProcessingStatsDoesNotRequireDependencies(t *testing.T) {
	w := httptest.NewRecorder()
	serveProcessingStats(w, httptest.NewRequest("GET", "/realtime-stats/processing", nil))
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"processing", "consumers", "diagnostics"} {
		if got[key] == nil {
			t.Fatalf("missing %s", key)
		}
	}
}
