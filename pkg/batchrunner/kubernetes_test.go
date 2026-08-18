package batchrunner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
		_, _ = w.Write([]byte(`{"items":[
			{"metadata":{"name":"completed-pod"},"status":{"phase":"Succeeded"}},
			{"metadata":{"name":"terminating-pod","deletionTimestamp":"2026-08-18T10:00:00Z"},"status":{"phase":"Running"}}
		]}`))
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

func TestPodReportsTerminatingStatus(t *testing.T) {
	pod := kubernetesPod{}
	pod.Metadata.DeletionTimestamp = "2026-07-20T12:00:00Z"
	pod.Status.Phase = "Running"
	if status := pod.status(); status != PodStatusTerminating {
		t.Fatalf("status = %q, want terminating", status)
	}
}

func TestStreamJobLogsCapturesEveryPodAttempt(t *testing.T) {
	var podListRequests int
	var attemptOneLogRequests int
	jobDone := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/namespaces/default/pods":
			podListRequests++
			w.Header().Set("Content-Type", "application/json")
			if podListRequests == 1 {
				_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"attempt-one","creationTimestamp":"2026-08-18T10:00:00Z"},"spec":{"nodeName":"node-a"},"status":{"phase":"Failed"}}]}`))
				return
			}
			_, _ = w.Write([]byte(`{"items":[
				{"metadata":{"name":"attempt-one","creationTimestamp":"2026-08-18T10:00:00Z"},"spec":{"nodeName":"node-a"},"status":{"phase":"Failed"}},
				{"metadata":{"name":"attempt-two","creationTimestamp":"2026-08-18T10:01:00Z"},"spec":{"nodeName":"node-b"},"status":{"phase":"Succeeded"}}
			]}`))
			close(jobDone)
		case r.URL.Path == "/api/v1/namespaces/default/pods/attempt-one/log":
			attemptOneLogRequests++
			if attemptOneLogRequests == 1 {
				return
			}
			_, _ = w.Write([]byte("2026-08-18T10:00:30Z first attempt failed\n"))
		case r.URL.Path == "/api/v1/namespaces/default/pods/attempt-two/log":
			_, _ = w.Write([]byte("2026-08-18T10:01:30Z replacement succeeded\n"))
		default:
			t.Fatalf("unexpected request path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	logPath := filepath.Join(t.TempDir(), "task.log")
	client := &kubernetesClient{
		namespace:        "default",
		baseURL:          server.URL,
		http:             server.Client(),
		logRetryInterval: time.Millisecond,
	}
	observations := map[string]PodObservation{}
	if err := client.streamJobLogs(context.Background(), "test-job", logPath, jobDone, func(observation PodObservation) {
		if observation.PodName != "" {
			observations[observation.PodName] = observation
		}
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	logText := string(data)
	for _, expected := range []string{
		"--- Kubernetes Job attempt: attempt-one ---",
		"first attempt failed",
		"--- Kubernetes Job attempt: attempt-two ---",
		"replacement succeeded",
	} {
		if !strings.Contains(logText, expected) {
			t.Fatalf("log %q does not contain %q", logText, expected)
		}
	}
	if observations["attempt-one"].NodeName != "node-a" || observations["attempt-two"].NodeName != "node-b" {
		t.Fatalf("observations = %#v", observations)
	}
}

func TestDoJSONRetriesTransientKubernetesFailures(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if requests < 3 {
			http.Error(w, "temporarily unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"succeeded":1}}`))
	}))
	defer server.Close()

	client := &kubernetesClient{
		baseURL:             server.URL,
		http:                server.Client(),
		apiRetryInterval:    time.Millisecond,
		apiRetryMaxInterval: 2 * time.Millisecond,
	}
	var job kubernetesJob
	if err := client.doJSON(context.Background(), http.MethodGet, "/job", nil, &job); err != nil {
		t.Fatal(err)
	}
	if requests != 3 || job.Status.Succeeded != 1 {
		t.Fatalf("requests = %d, job = %#v", requests, job)
	}
}

func TestRecoveringExecutorReconcilesCompletedJob(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/apis/batch/v1/namespaces/default/jobs/completed-job":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":{"conditions":[{"type":"Complete","status":"True"}]}}`))
		case "/api/v1/namespaces/default/pods":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"completed-pod","creationTimestamp":"2026-08-18T10:00:00Z"},"spec":{"nodeName":"node-a"},"status":{"phase":"Succeeded","containerStatuses":[{"name":"main","state":{"terminated":{"exitCode":0,"reason":"Completed","startedAt":"2026-08-18T10:00:01Z","finishedAt":"2026-08-18T10:01:00Z"}}}]}}]}`))
		case "/api/v1/namespaces/default/pods/completed-pod/log":
			_, _ = w.Write([]byte("2026-08-18T10:00:30Z completed output\n"))
		default:
			t.Fatalf("unexpected recovery request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	executor := &KubernetesExecutor{
		config: Config{Namespace: "default"},
		client: &kubernetesClient{namespace: "default", baseURL: server.URL, http: server.Client()},
	}
	logPath := filepath.Join(t.TempDir(), "task.log")
	observations := []PodObservation{}
	exitCode, err := executor.RunTask(context.Background(), "run", &Task{ID: "task", JobName: "completed-job"}, logPath, true, func(observation PodObservation) {
		observations = append(observations, observation)
	})
	if err != nil || exitCode != 0 {
		t.Fatalf("exit code = %d, error = %v", exitCode, err)
	}
	if len(observations) != 1 || observations[0].PodName != "completed-pod" || observations[0].ExitCode == nil || *observations[0].ExitCode != 0 {
		t.Fatalf("observations = %#v", observations)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "completed output") {
		t.Fatalf("log = %q", data)
	}
}

func TestJobOutcomeUsesFailedConditionMessage(t *testing.T) {
	job := &kubernetesJob{}
	job.Status.Conditions = make([]struct {
		Type    string `json:"type"`
		Status  string `json:"status"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}, 1)
	job.Status.Conditions[0].Type = "Failed"
	job.Status.Conditions[0].Status = "True"
	job.Status.Conditions[0].Reason = "BackoffLimitExceeded"
	job.Status.Conditions[0].Message = "pod failed twice"
	terminal, exitCode, err := jobOutcome("failed-job", job)
	if !terminal || exitCode != 1 || err == nil || err.Error() != "pod failed twice" {
		t.Fatalf("terminal = %t, exit code = %d, error = %v", terminal, exitCode, err)
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
