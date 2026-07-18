package batchrunner

import (
	"context"
	"encoding/json"
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

func TestCreateJobSchedulesSmallTasksOnDefaultNodes(t *testing.T) {
	podSpec := createJobPodSpec(t, &Task{ID: "small-task", Size: "small"})
	if _, ok := podSpec["nodeSelector"]; ok {
		t.Fatal("small task unexpectedly has a node selector")
	}
	if _, ok := podSpec["tolerations"]; ok {
		t.Fatal("small task unexpectedly has a toleration")
	}
}

func TestCreateJobSchedulesNonSmallTasksOnBatchImportNodes(t *testing.T) {
	podSpec := createJobPodSpec(t, &Task{ID: "medium-task", Size: "medium"})
	nodeSelector, ok := podSpec["nodeSelector"].(map[string]any)
	if !ok || nodeSelector["workload"] != "batch-import" {
		t.Fatalf("node selector = %#v, want workload=batch-import", podSpec["nodeSelector"])
	}
	tolerations, ok := podSpec["tolerations"].([]any)
	if !ok || len(tolerations) != 1 {
		t.Fatalf("tolerations = %#v, want one batch-import toleration", podSpec["tolerations"])
	}
	toleration, ok := tolerations[0].(map[string]any)
	if !ok || toleration["key"] != "workload" || toleration["operator"] != "Equal" || toleration["value"] != "batch-import" || toleration["effect"] != "NoSchedule" {
		t.Fatalf("toleration = %#v, want workload=batch-import:NoSchedule", tolerations[0])
	}
}

func createJobPodSpec(t *testing.T, task *Task) map[string]any {
	t.Helper()
	var job map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("request method = %s, want POST", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&job); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := &kubernetesClient{namespace: "default", baseURL: server.URL, http: server.Client()}
	if err := client.createJob(context.Background(), Config{}, "test-job", "test-run", task); err != nil {
		t.Fatal(err)
	}

	return job["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
}
