package batchrunner

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestStageArtifactServeAndRemove(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.StageArtifact("run-123", func(file *os.File) (bool, string, error) {
		_, err := file.WriteString("artifact contents")
		return true, `"source-v1"`, err
	})
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || len(token) != 48 {
		t.Fatalf("token = %q, want opaque 48 character token", token)
	}

	handler := store.ArtifactHandler()
	path := "/artifacts/run-123/" + token
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	response := recorder.Result()
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || string(body) != "artifact contents" {
		t.Fatalf("GET = %d %q", response.StatusCode, body)
	}
	if response.Header.Get("ETag") != `"source-v1"` || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatalf("headers = %#v", response.Header)
	}

	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	request.Header.Set("If-None-Match", `"source-v1"`)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", recorder.Code)
	}

	if err := store.RemoveArtifact("run-123", token); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("removed artifact status = %d, want 404", recorder.Code)
	}
}

func TestStageArtifactUnchangedAndPathValidation(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	token, err := store.StageArtifact("run-123", func(file *os.File) (bool, string, error) {
		_, err := file.WriteString("discard me")
		return false, "etag", err
	})
	if err != nil || token != "" {
		t.Fatalf("unchanged StageArtifact = (%q, %v), want empty token and nil error", token, err)
	}
	if _, err := os.Stat(store.Root() + "/artifacts/run-123"); err != nil {
		t.Fatalf("stage directory should exist but no artifact published: %v", err)
	}
	for _, path := range []string{
		"/artifacts/../" + strings.Repeat("a", 48),
		"/artifacts/run-123/../../etc/passwd",
		"/artifacts/run-123/" + strings.Repeat("z", 48),
	} {
		recorder := httptest.NewRecorder()
		store.ArtifactHandler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Errorf("path %q status = %d, want 404", path, recorder.Code)
		}
	}
	if err := store.RemoveRunArtifacts("../run-123"); err == nil {
		t.Fatal("RemoveRunArtifacts accepted an unsafe run ID")
	}
}
