package batchrunner

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerIsAPIOnlyWithoutDuplicateAPIPrefix(t *testing.T) {
	handler := NewServer(nil, nil).Handler()

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/plan", nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected /plan to route to the plan handler, got %d", response.Code)
	}

	for _, path := range []string{"/", "/api/plan"} {
		response = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, path, nil)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("expected %s to return 404, got %d", path, response.Code)
		}
	}
}
