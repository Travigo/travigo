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
	if _, err := client.findRunningJobPod(context.Background(), "existing-job", nil); err == nil {
		t.Fatal("expected non-running pod to prevent recovery")
	}
}

func TestFindJobPodReportsTerminatingStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"terminating-pod","deletionTimestamp":"2026-07-20T12:00:00Z"},"status":{"phase":"Running"}}]}`))
	}))
	defer server.Close()

	client := &kubernetesClient{namespace: "default", baseURL: server.URL, http: server.Client()}
	podName, status, err := client.findJobPod(context.Background(), "existing-job")
	if err != nil {
		t.Fatal(err)
	}
	if podName != "terminating-pod" || status != PodStatusTerminating {
		t.Fatalf("pod = %q, status = %q; want terminating-pod, terminating", podName, status)
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

func TestChildJobReceivesTfLAPIKeySecret(t *testing.T) {
	environment := childJobEnv(Config{TfLAPIKeySecret: "custom-tfl-secret"})
	for _, variable := range environment {
		if variable["name"] != "TRAVIGO_TFL_API_KEY" {
			continue
		}
		valueFrom := variable["valueFrom"].(map[string]any)
		secretRef := valueFrom["secretKeyRef"].(map[string]any)
		if secretRef["name"] != "custom-tfl-secret" || secretRef["key"] != "api_key" {
			t.Fatalf("TfL secret ref = %#v", secretRef)
		}
		return
	}
	t.Fatal("TRAVIGO_TFL_API_KEY was not added to child job environment")
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

	template := job["spec"].(map[string]any)["template"].(map[string]any)
	annotations := template["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"] != "false" {
		t.Fatalf("safe-to-evict annotation = %#v, want false", annotations["cluster-autoscaler.kubernetes.io/safe-to-evict"])
	}
	return template["spec"].(map[string]any)
}

func TestEnsurePodDisruptionBudgetProtectsOneJobPod(t *testing.T) {
	var budget map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/apis/policy/v1/namespaces/default/poddisruptionbudgets" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&budget); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	client := &kubernetesClient{namespace: "default", baseURL: server.URL, http: server.Client()}
	if err := client.ensurePodDisruptionBudget(context.Background(), "test-job"); err != nil {
		t.Fatal(err)
	}

	spec := budget["spec"].(map[string]any)
	if spec["minAvailable"] != float64(1) {
		t.Fatalf("minAvailable = %#v, want 1", spec["minAvailable"])
	}
	labels := spec["selector"].(map[string]any)["matchLabels"].(map[string]any)
	if labels["job-name"] != "test-job" {
		t.Fatalf("PDB selector = %#v, want job-name=test-job", labels)
	}
}
