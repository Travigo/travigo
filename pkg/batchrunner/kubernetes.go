package batchrunner

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type TaskExecutor interface {
	RunTask(ctx context.Context, runID string, task *Task, logPath string, recovering bool, updatePodStatus func(PodStatus)) (int, error)
	DeleteJob(ctx context.Context, name string) error
}

type KubernetesExecutor struct {
	config Config
	client *kubernetesClient
}

func NewKubernetesExecutor(config Config) (*KubernetesExecutor, error) {
	client, err := newKubernetesClient(config.Namespace)
	if err != nil {
		return nil, err
	}

	return &KubernetesExecutor{
		config: config,
		client: client,
	}, nil
}

func (e *KubernetesExecutor) RunTask(ctx context.Context, runID string, task *Task, logPath string, recovering bool, updatePodStatus func(PodStatus)) (int, error) {
	jobName := task.JobName
	if jobName == "" {
		jobName = jobNameForTask(runID, task.ID)
	}
	task.JobName = jobName
	if err := e.client.ensurePodDisruptionBudget(ctx, jobName); err != nil {
		return 1, err
	}
	defer func() {
		_ = e.client.deletePodDisruptionBudget(context.Background(), jobName)
	}()

	var podName string
	var err error
	if recovering {
		podName, err = e.client.findRunningJobPod(ctx, jobName, updatePodStatus)
	} else {
		if err := e.client.ensureJob(ctx, e.config, jobName, runID, task); err != nil {
			return 1, err
		}
		podName, err = e.client.waitForJobPod(ctx, jobName, updatePodStatus)
	}
	if err != nil {
		return 1, err
	}

	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		e.client.streamPodLogs(ctx, podName, logPath)
	}()

	exitCode, waitErr := e.client.waitForJobCompletion(ctx, jobName, updatePodStatus)
	select {
	case <-logDone:
	case <-time.After(10 * time.Second):
	}

	if ctx.Err() != nil {
		reportPodStatus(updatePodStatus, PodStatusTerminating)
		_ = e.DeleteJob(context.Background(), jobName)
		return 1, ctx.Err()
	}

	if waitErr != nil {
		reportPodStatus(updatePodStatus, PodStatusFailed)
		return exitCode, waitErr
	}

	reportPodStatus(updatePodStatus, PodStatusSucceeded)
	return exitCode, nil
}

func (c *kubernetesClient) ensureJob(ctx context.Context, config Config, jobName string, runID string, task *Task) error {
	job, err := c.getJob(ctx, jobName)
	if err != nil {
		return err
	}
	if job != nil {
		return nil
	}

	err = c.createJob(ctx, config, jobName, runID, task)
	if isKubernetesStatus(err, http.StatusConflict) {
		return nil
	}
	return err
}

func (e *KubernetesExecutor) DeleteJob(ctx context.Context, name string) error {
	return e.client.deleteJob(ctx, name)
}

type kubernetesClient struct {
	namespace string
	baseURL   string
	token     string
	http      *http.Client
}

func newKubernetesClient(namespace string) (*kubernetesClient, error) {
	host := os.Getenv("KUBERNETES_SERVICE_HOST")
	port := os.Getenv("KUBERNETES_SERVICE_PORT")
	if host == "" || port == "" {
		return nil, errors.New("KUBERNETES_SERVICE_HOST and KUBERNETES_SERVICE_PORT must be set")
	}

	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		return nil, err
	}

	roots := x509.NewCertPool()
	if ca, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"); err == nil {
		roots.AppendCertsFromPEM(ca)
	}

	return &kubernetesClient{
		namespace: namespace,
		baseURL:   "https://" + host + ":" + port,
		token:     strings.TrimSpace(string(token)),
		http: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12},
			},
		},
	}, nil
}

func (c *kubernetesClient) createJob(ctx context.Context, config Config, jobName string, runID string, task *Task) error {
	podSpec := map[string]any{
		"restartPolicy":      "Never",
		"serviceAccountName": config.JobServiceAccountName,
		"containers": []map[string]any{
			{
				"name":            "main",
				"image":           config.JobImage,
				"imagePullPolicy": config.JobImagePullPolicy,
				"args":            task.Args,
				"env":             childJobEnv(config),
			},
		},
	}
	if task.Size != "small" {
		podSpec["nodeSelector"] = map[string]string{"workload": "batch-import"}
		podSpec["tolerations"] = []map[string]string{{
			"key":      "workload",
			"operator": "Equal",
			"value":    "batch-import",
			"effect":   "NoSchedule",
		}}
	}

	body := map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":   jobName,
			"labels": jobLabels(runID, task.ID),
		},
		"spec": map[string]any{
			"backoffLimit":            config.JobBackoffLimit,
			"ttlSecondsAfterFinished": config.JobTTLSeconds,
			"template": map[string]any{
				"metadata": map[string]any{
					"labels":      jobLabels(runID, task.ID),
					"annotations": map[string]string{"cluster-autoscaler.kubernetes.io/safe-to-evict": "false"},
				},
				"spec": podSpec,
			},
		},
	}

	if config.JobActiveDeadlineSeconds > 0 {
		body["spec"].(map[string]any)["activeDeadlineSeconds"] = config.JobActiveDeadlineSeconds
	}

	return c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs", c.namespace), body, nil)
}

func (c *kubernetesClient) ensurePodDisruptionBudget(ctx context.Context, jobName string) error {
	body := map[string]any{
		"apiVersion": "policy/v1",
		"kind":       "PodDisruptionBudget",
		"metadata": map[string]any{
			"name": jobName,
			"labels": map[string]string{
				"app.kubernetes.io/name": "travigo-batch-runner",
			},
		},
		"spec": map[string]any{
			"minAvailable": 1,
			"selector": map[string]any{
				"matchLabels": map[string]string{"job-name": jobName},
			},
		},
	}
	err := c.doJSON(ctx, http.MethodPost, fmt.Sprintf("/apis/policy/v1/namespaces/%s/poddisruptionbudgets", c.namespace), body, nil)
	if isKubernetesStatus(err, http.StatusConflict) {
		return nil
	}
	return err
}

func (c *kubernetesClient) getJob(ctx context.Context, jobName string) (*kubernetesJob, error) {
	var job kubernetesJob
	err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", c.namespace, jobName), nil, &job)
	if isKubernetesStatus(err, http.StatusNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &job, nil
}

func (c *kubernetesClient) waitForJobPod(ctx context.Context, jobName string, updatePodStatus func(PodStatus)) (string, error) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		podName, status, err := c.findJobPod(ctx, jobName)
		if err != nil {
			return "", err
		}
		reportPodStatus(updatePodStatus, status)
		if podName != "" {
			return podName, nil
		}

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *kubernetesClient) waitForJobCompletion(ctx context.Context, jobName string, updatePodStatus func(PodStatus)) (int, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		_, status, err := c.findJobPod(ctx, jobName)
		if err != nil {
			return 1, err
		}
		reportPodStatus(updatePodStatus, status)

		var job kubernetesJob
		if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s", c.namespace, jobName), nil, &job); err != nil {
			return 1, err
		}

		if job.Status.Succeeded > 0 {
			return 0, nil
		}
		for _, condition := range job.Status.Conditions {
			if condition.Type == "Failed" && condition.Status == "True" {
				if condition.Message != "" {
					return 1, errors.New(condition.Message)
				}
				return 1, fmt.Errorf("job %s failed", jobName)
			}
			if condition.Type == "Complete" && condition.Status == "True" {
				return 0, nil
			}
		}

		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *kubernetesClient) findJobPod(ctx context.Context, jobName string) (string, PodStatus, error) {
	pods, err := c.listJobPods(ctx, jobName)
	if err != nil {
		return "", "", err
	}
	if len(pods.Items) == 0 {
		return "", PodStatusPending, nil
	}

	pod := pods.Items[0]
	return pod.Metadata.Name, pod.status(), nil
}

func (c *kubernetesClient) findRunningJobPod(ctx context.Context, jobName string, updatePodStatus func(PodStatus)) (string, error) {
	pods, err := c.listJobPods(ctx, jobName)
	if err != nil {
		return "", err
	}
	for _, pod := range pods.Items {
		reportPodStatus(updatePodStatus, pod.status())
		if pod.Status.Phase == "Running" {
			return pod.Metadata.Name, nil
		}
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("cannot resume job %s: no running pod exists", jobName)
	}
	return "", fmt.Errorf("cannot resume job %s: pod is not running", jobName)
}

func reportPodStatus(updatePodStatus func(PodStatus), status PodStatus) {
	if updatePodStatus != nil && status != "" {
		updatePodStatus(status)
	}
}

func (c *kubernetesClient) listJobPods(ctx context.Context, jobName string) (*kubernetesPodList, error) {
	var pods kubernetesPodList
	path := fmt.Sprintf(
		"/api/v1/namespaces/%s/pods?labelSelector=%s",
		c.namespace,
		url.QueryEscape("job-name="+jobName),
	)
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &pods); err != nil {
		return nil, err
	}
	return &pods, nil
}

func (c *kubernetesClient) streamPodLogs(ctx context.Context, podName string, logPath string) {
	for {
		err := c.copyPodLogs(ctx, podName, logPath, true)
		if err == nil || ctx.Err() != nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
}

func (c *kubernetesClient) copyPodLogs(ctx context.Context, podName string, logPath string, follow bool) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()

	query := url.Values{}
	query.Set("container", "main")
	if follow {
		query.Set("follow", "true")
	}

	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?%s", c.namespace, podName, query.Encode()), nil)
	if err != nil {
		return err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("kubernetes logs request failed: %s: %s", resp.Status, string(data))
	}

	_, err = io.Copy(file, resp.Body)
	return err
}

func (c *kubernetesClient) deleteJob(ctx context.Context, name string) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s?propagationPolicy=Background", c.namespace, name)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *kubernetesClient) deletePodDisruptionBudget(ctx context.Context, name string) error {
	path := fmt.Sprintf("/apis/policy/v1/namespaces/%s/poddisruptionbudgets/%s", c.namespace, name)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *kubernetesClient) doJSON(ctx context.Context, method string, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(data)
	}

	req, err := c.newRequest(ctx, method, path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return &kubernetesAPIError{statusCode: resp.StatusCode, message: fmt.Sprintf("kubernetes request failed: %s %s: %s: %s", method, path, resp.Status, string(data))}
	}

	if out == nil {
		return nil
	}

	return json.NewDecoder(resp.Body).Decode(out)
}

type kubernetesAPIError struct {
	statusCode int
	message    string
}

func (e *kubernetesAPIError) Error() string {
	return e.message
}

func isKubernetesStatus(err error, statusCode int) bool {
	var apiErr *kubernetesAPIError
	return errors.As(err, &apiErr) && apiErr.statusCode == statusCode
}

func (c *kubernetesClient) newRequest(ctx context.Context, method string, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func jobNameForTask(runID string, taskID string) string {
	name := "travigo-batch-" + dnsNamePart(runID) + "-" + dnsNamePart(taskID)
	if len(name) <= 63 {
		return name
	}

	sum := sha1.Sum([]byte(runID + ":" + taskID))
	suffix := hex.EncodeToString(sum[:])[:8]
	prefix := strings.Trim(name[:63-len(suffix)-1], "-.")
	if prefix == "" {
		prefix = "travigo-batch"
	}

	return prefix + "-" + suffix
}

func dnsNamePart(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '-', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteRune('-')
		}
	}

	cleaned := strings.Trim(builder.String(), "-.")
	if cleaned == "" {
		return "x"
	}

	return cleaned
}

func jobLabels(runID string, taskID string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name": "travigo-batch-runner",
		"travigo.io/batch-run":   sanitizePathPart(runID),
		"travigo.io/batch-task":  sanitizePathPart(taskID),
	}
}

func childJobEnv(config Config) []map[string]any {
	return []map[string]any{
		valueEnv("TRAVIGO_LOG_FORMAT", "JSON"),
		secretEnv("TRAVIGO_BODS_API_KEY", config.BodsAPIKeySecret, "api_key", false),
		secretEnv("TRAVIGO_TFL_API_KEY", config.TfLAPIKeySecret, "api_key", false),
		secretEnv("TRAVIGO_IE_NATIONALTRANSPORT_API_KEY", config.IENationalTransportAPIKeySecret, "api_key", false),
		secretEnv("TRAVIGO_NATIONALRAIL_USERNAME", config.NationalRailCredentialsSecret, "username", true),
		secretEnv("TRAVIGO_NATIONALRAIL_PASSWORD", config.NationalRailCredentialsSecret, "password", true),
		secretEnv("TRAVIGO_NETWORKRAIL_USERNAME", config.NetworkRailCredentialsSecret, "username", false),
		secretEnv("TRAVIGO_NETWORKRAIL_PASSWORD", config.NetworkRailCredentialsSecret, "password", false),
		secretEnv("TRAVIGO_SE_TRAFIKLAB_STATIC_API_KEY", config.TrafiklabStaticSecret, "api_key", false),
		secretEnv("TRAVIGO_SE_TRAFIKLAB_REALTIME_API_KEY", config.TrafiklabRealtimeSecret, "api_key", false),
		secretEnv("TRAVIGO_JP_ODPT_API_KEY", config.OdptAPIKeySecret, "api_key", false),
		secretEnv("TRAVIGO_MONGODB_CONNECTION", config.MongoDBConnectionSecret, "connectionString.standard", false),
		valueEnv("TRAVIGO_MONGODB_DATABASE", config.MongoDBDatabase),
		valueEnv("TRAVIGO_ELASTICSEARCH_ADDRESS", config.ElasticsearchAddress),
		secretEnv("TRAVIGO_ELASTICSEARCH_USERNAME", config.ElasticsearchUserSecret, "username", false),
		secretEnv("TRAVIGO_ELASTICSEARCH_PASSWORD", config.ElasticsearchUserSecret, "password", false),
		valueEnv("TRAVIGO_REDIS_ADDRESS", config.RedisAddress),
		secretEnv("TRAVIGO_REDIS_PASSWORD", config.RedisPasswordSecret, "password", false),
	}
}

func valueEnv(name string, value string) map[string]any {
	return map[string]any{"name": name, "value": value}
}

func secretEnv(name string, secret string, key string, optional bool) map[string]any {
	return map[string]any{
		"name": name,
		"valueFrom": map[string]any{
			"secretKeyRef": map[string]any{
				"name":     secret,
				"key":      key,
				"optional": optional,
			},
		},
	}
}

type kubernetesJob struct {
	Status struct {
		Succeeded  int `json:"succeeded"`
		Failed     int `json:"failed"`
		Conditions []struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Reason  string `json:"reason"`
			Message string `json:"message"`
		} `json:"conditions"`
	} `json:"status"`
}

type kubernetesPodList struct {
	Items []kubernetesPod `json:"items"`
}

type kubernetesPod struct {
	Metadata struct {
		Name              string `json:"name"`
		DeletionTimestamp string `json:"deletionTimestamp"`
	} `json:"metadata"`
	Status struct {
		Phase string `json:"phase"`
	} `json:"status"`
}

func (p kubernetesPod) status() PodStatus {
	if p.Metadata.DeletionTimestamp != "" {
		return PodStatusTerminating
	}
	if p.Status.Phase == "" {
		return PodStatusPending
	}
	return PodStatus(strings.ToLower(p.Status.Phase))
}
