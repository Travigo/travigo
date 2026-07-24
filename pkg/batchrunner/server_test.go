package batchrunner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestServerIsAPIOnlyWithoutDuplicateAPIPrefix(t *testing.T) {
	handler := NewServer(nil, nil).handlerWithTokenValidator(adminTokenValidator)

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/plan", nil)
	request.Header.Set("Authorization", "Bearer test")
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected /plan to route to the plan handler, got %d", response.Code)
	}

	for _, path := range []string{"/", "/api/plan"} {
		response = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer test")
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf("expected %s to return 404, got %d", path, response.Code)
		}
	}
}

func TestServerRequiresAdminPermission(t *testing.T) {
	t.Setenv("TRAVIGO_BATCH_RUNNER_AUTH_TOKEN", "")
	handler := NewServer(nil, nil).handlerWithTokenValidator(func(_ context.Context, token string) ([]string, error) {
		switch token {
		case "admin":
			return []string{"admin:all"}, nil
		case "member":
			return []string{"read:all"}, nil
		default:
			return nil, errors.New("invalid token")
		}
	})

	for _, test := range []struct {
		name          string
		authorization string
		status        int
	}{
		{name: "missing token", status: http.StatusUnauthorized},
		{name: "invalid scheme", authorization: "Basic admin", status: http.StatusUnauthorized},
		{name: "invalid token", authorization: "Bearer invalid", status: http.StatusUnauthorized},
		{name: "non-admin token", authorization: "Bearer member", status: http.StatusUnauthorized},
		{name: "admin token", authorization: "Bearer admin", status: http.StatusNotFound},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/not-found", nil)
			request.Header.Set("Authorization", test.authorization)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("expected %d, got %d", test.status, response.Code)
			}
		})
	}
}

func TestServerAcceptsInternalBatchRunnerToken(t *testing.T) {
	t.Setenv("TRAVIGO_BATCH_RUNNER_AUTH_TOKEN", "internal-token")
	handler := NewServer(nil, nil).handlerWithTokenValidator(func(_ context.Context, _ string) ([]string, error) {
		return nil, errors.New("JWT validation should not run")
	})

	request := httptest.NewRequest(http.MethodGet, "/not-found", nil)
	request.Header.Set("Authorization", "Bearer internal-token")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected internal token to be accepted, got %d", response.Code)
	}
}

func adminTokenValidator(_ context.Context, _ string) ([]string, error) {
	return []string{"admin:all"}, nil
}
