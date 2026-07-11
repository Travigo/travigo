package batchrunner

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureJobReusesExistingJob(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != http.MethodGet {
			t.Fatalf("request method = %s, want GET", r.Method)
		}
		if r.URL.Path != "/apis/batch/v1/namespaces/default/jobs/existing-job" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{}}`))
	}))
	defer server.Close()

	client := &kubernetesClient{
		namespace: "default",
		baseURL:   server.URL,
		http:      server.Client(),
	}
	err := client.ensureJob(context.Background(), Config{}, "existing-job", "run-id", &Task{ID: "task-id"})
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
}

func TestFindRunningJobPodRequiresRunningPod(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/namespaces/default/pods" {
			t.Fatalf("request path = %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"completed-pod"},"status":{"phase":"Succeeded"}}]}`))
	}))
	defer server.Close()

	client := &kubernetesClient{
		namespace: "default",
		baseURL:   server.URL,
		http:      server.Client(),
	}
	if _, err := client.findRunningJobPod(context.Background(), "existing-job"); err == nil {
		t.Fatal("expected non-running pod to prevent recovery")
	}
}
