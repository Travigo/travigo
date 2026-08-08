package batchrunner

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
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

func TestRunSummaryViewExcludesTasksAndOptions(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRun(&Run{
		ID: "run-1", Status: RunStatusRunning, CreatedAt: time.Now(),
		Options: RunOptions{TaskIDs: []string{"task-1"}},
		Tasks:   []Task{{ID: "task-1", Name: "Large task", Args: []string{"unused", "arguments"}}},
	}); err != nil {
		t.Fatal(err)
	}

	handler := NewServer(store, nil).handlerWithTokenValidator(adminTokenValidator)
	request := httptest.NewRequest(http.MethodGet, "/runs?view=summary", nil)
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	var runs []map[string]interface{}
	if err := json.Unmarshal(response.Body.Bytes(), &runs); err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	for _, key := range []string{"tasks", "options", "cancelRequested"} {
		if _, ok := runs[0][key]; ok {
			t.Errorf("summary unexpectedly contains %q", key)
		}
	}
	for _, key := range []string{"id", "status", "createdAt"} {
		if _, ok := runs[0][key]; !ok {
			t.Errorf("summary is missing %q", key)
		}
	}
}

func TestTaskLogSupportsIncrementalOffsets(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{ID: "run-1", Tasks: []Task{{ID: "task-1"}}}
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}
	logPath, err := store.PrepareLog(run.ID, run.Tasks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logPath, []byte("first\nsecond\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	handler := NewServer(store, nil).handlerWithTokenValidator(adminTokenValidator)
	request := httptest.NewRequest(http.MethodGet, "/runs/run-1/tasks/task-1/log?offset=6", nil)
	request.Header.Set("Authorization", "Bearer test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if got := response.Body.String(); got != "second\n" {
		t.Fatalf("body = %q, want second line", got)
	}
	if got := response.Header().Get("X-Log-Next-Offset"); got != "13" {
		t.Fatalf("next offset = %q, want 13", got)
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
