package batchrunner

import (
	"bufio"
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
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

type TaskExecutor interface {
	RunTask(ctx context.Context, runID string, task *Task, logPath string, recovering bool, updatePod func(PodObservation)) (int, error)
	DeleteJob(ctx context.Context, name string) error
}

type PodObservation struct {
	PodName    string
	NodeName   string
	Status     PodStatus
	CreatedAt  *time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
	ExitCode   *int
	Reason     string
	Message    string
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

func (e *KubernetesExecutor) RunTask(ctx context.Context, runID string, task *Task, logPath string, recovering bool, updatePod func(PodObservation)) (int, error) {
	jobName := task.JobName
	if jobName == "" {
		jobName = jobNameForTask(runID, task.ID)
	}
	task.JobName = jobName

	if recovering {
		job, err := e.client.getJob(ctx, jobName)
		if err != nil {
			return 1, err
		}
		if job == nil {
			return 1, fmt.Errorf("cannot resume job %s: job does not exist", jobName)
		}
		if terminal, exitCode, outcomeErr := jobOutcome(jobName, job); terminal {
			e.client.captureFinishedJobLogs(ctx, jobName, logPath, updatePod)
			return exitCode, outcomeErr
		}
		// Active recovery remains deliberately strict: only a genuinely Running
		// Pod is safe to reattach. Pending or terminal Pods are reconciled, not
		// treated as resumable work.
		if _, err := e.client.findRunningJobPod(ctx, jobName, updatePod); err != nil {
			return 1, err
		}
	} else {
		if err := e.client.ensurePodDisruptionBudget(ctx, jobName); err != nil {
			return 1, err
		}
		if err := e.client.ensureJob(ctx, e.config, jobName, runID, task); err != nil {
			e.client.cleanupPodDisruptionBudget(jobName)
			return 1, err
		}
		defer e.client.cleanupPodDisruptionBudget(jobName)
	}
	if recovering {
		if err := e.client.ensurePodDisruptionBudget(ctx, jobName); err != nil {
			return 1, err
		}
		defer e.client.cleanupPodDisruptionBudget(jobName)
	}

	logContext, cancelLog := context.WithCancel(ctx)
	defer cancelLog()
	jobDone := make(chan struct{})
	logDone := make(chan error, 1)
	go func() {
		logDone <- e.client.streamJobLogs(logContext, jobName, logPath, jobDone, updatePod)
	}()

	exitCode, waitErr := e.client.waitForJobCompletion(ctx, jobName, updatePod)
	close(jobDone)
	select {
	case logErr := <-logDone:
		if logErr != nil {
			log.Warn().Err(logErr).Str("job", jobName).Msg("Job log collection ended with an error")
		}
	case <-time.After(10 * time.Second):
		log.Warn().Str("job", jobName).Msg("Timed out waiting for job log collection to finish")
	}
	cancelLog()

	if ctx.Err() != nil {
		reportPodStatus(updatePod, PodStatusTerminating)
		deleteContext, cancelDelete := context.WithTimeout(context.Background(), 30*time.Second)
		_ = e.DeleteJob(deleteContext, jobName)
		cancelDelete()
		return 1, ctx.Err()
	}

	if waitErr != nil {
		// A read can fail just as the Job reaches a terminal state. Re-read the
		// authoritative Job before recording an infrastructure error.
		if job, reconcileErr := e.client.getJob(ctx, jobName); reconcileErr == nil && job != nil {
			if terminal, reconciledExitCode, outcomeErr := jobOutcome(jobName, job); terminal {
				return reconciledExitCode, outcomeErr
			}
		}
		reportPodStatus(updatePod, PodStatusFailed)
		return exitCode, waitErr
	}

	reportPodStatus(updatePod, PodStatusSucceeded)
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

	logRetryInterval    time.Duration
	apiRetryInterval    time.Duration
	apiRetryMaxInterval time.Duration
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
			Timeout: 30 * time.Second,
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

func (c *kubernetesClient) waitForJobCompletion(ctx context.Context, jobName string, updatePod func(PodObservation)) (int, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		pods, err := c.listJobPods(ctx, jobName)
		if err != nil {
			return 1, err
		}
		sortPods(pods.Items)
		for _, pod := range pods.Items {
			reportPod(updatePod, pod.observation())
		}

		job, err := c.getJob(ctx, jobName)
		if err != nil {
			return 1, err
		}
		if job == nil {
			return 1, fmt.Errorf("job %s disappeared before completion", jobName)
		}
		if terminal, exitCode, outcomeErr := jobOutcome(jobName, job); terminal {
			return exitCode, outcomeErr
		}

		select {
		case <-ctx.Done():
			return 1, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (c *kubernetesClient) findRunningJobPod(ctx context.Context, jobName string, updatePod func(PodObservation)) (string, error) {
	pods, err := c.listJobPods(ctx, jobName)
	if err != nil {
		return "", err
	}
	sortPods(pods.Items)
	runningPod := ""
	for _, pod := range pods.Items {
		reportPod(updatePod, pod.observation())
		if pod.Status.Phase == "Running" && pod.Metadata.DeletionTimestamp == "" {
			runningPod = pod.Metadata.Name
		}
	}
	if runningPod != "" {
		return runningPod, nil
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("cannot resume job %s: no running pod exists", jobName)
	}
	return "", fmt.Errorf("cannot resume job %s: pod is not running", jobName)
}

func reportPodStatus(updatePod func(PodObservation), status PodStatus) {
	reportPod(updatePod, PodObservation{Status: status})
}

func reportPod(updatePod func(PodObservation), observation PodObservation) {
	if updatePod != nil && (observation.PodName != "" || observation.Status != "") {
		updatePod(observation)
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

func (c *kubernetesClient) streamJobLogs(ctx context.Context, jobName string, logPath string, jobDone <-chan struct{}, updatePod func(PodObservation)) error {
	lastTimestampByPod := map[string]time.Time{}
	seenPods := map[string]struct{}{}
	for {
		pods, err := c.listJobPods(ctx, jobName)
		if err == nil {
			sortPods(pods.Items)
			for _, pod := range pods.Items {
				reportPod(updatePod, pod.observation())
				if _, seen := seenPods[pod.Metadata.Name]; !seen {
					if headerErr := appendPodAttemptHeader(logPath, pod.Metadata.Name); headerErr != nil {
						return headerErr
					}
					seenPods[pod.Metadata.Name] = struct{}{}
				}
				if pod.status() == PodStatusPending {
					continue
				}
				nextTimestamp, logErr := c.copyPodLogs(ctx, pod.Metadata.Name, logPath, false, lastTimestampByPod[pod.Metadata.Name])
				if nextTimestamp.After(lastTimestampByPod[pod.Metadata.Name]) {
					lastTimestampByPod[pod.Metadata.Name] = nextTimestamp
				}
				if logErr != nil && ctx.Err() == nil {
					log.Warn().Err(logErr).Str("job", jobName).Str("pod", pod.Metadata.Name).Msg("Retrying pod log collection")
				}
			}
		} else if ctx.Err() == nil {
			log.Warn().Err(err).Str("job", jobName).Msg("Retrying job pod discovery for log collection")
		}

		select {
		case <-ctx.Done():
			return nil
		case <-jobDone:
			return err
		default:
		}

		retryInterval := c.logRetryInterval
		if retryInterval <= 0 {
			retryInterval = 2 * time.Second
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-jobDone:
			timer.Stop()
			// Run one final non-following pass so logs written immediately before
			// Job completion are not lost.
			pods, finalErr := c.listJobPods(ctx, jobName)
			if finalErr != nil {
				return finalErr
			}
			sortPods(pods.Items)
			for _, pod := range pods.Items {
				reportPod(updatePod, pod.observation())
				if _, seen := seenPods[pod.Metadata.Name]; !seen {
					if headerErr := appendPodAttemptHeader(logPath, pod.Metadata.Name); headerErr != nil {
						return headerErr
					}
				}
				if pod.status() == PodStatusPending {
					continue
				}
				if _, logErr := c.copyPodLogs(ctx, pod.Metadata.Name, logPath, false, lastTimestampByPod[pod.Metadata.Name]); logErr != nil {
					return logErr
				}
			}
			return nil
		case <-timer.C:
		}
	}
}

func (c *kubernetesClient) captureFinishedJobLogs(ctx context.Context, jobName string, logPath string, updatePod func(PodObservation)) {
	jobDone := make(chan struct{})
	close(jobDone)
	if err := c.streamJobLogs(ctx, jobName, logPath, jobDone, updatePod); err != nil {
		log.Warn().Err(err).Str("job", jobName).Msg("Could not capture logs while reconciling finished Job")
	}
}

func appendPodAttemptHeader(logPath string, podName string) error {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return err
	}
	defer file.Close()
	header := fmt.Sprintf("--- Kubernetes Job attempt: %s ---", podName)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		if scanner.Text() == header {
			return nil
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	_, err = fmt.Fprintf(file, "\n%s\n", header)
	return err
}

func (c *kubernetesClient) copyPodLogs(ctx context.Context, podName string, logPath string, follow bool, since time.Time) (time.Time, error) {
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return since, err
	}

	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return since, err
	}
	defer file.Close()

	query := url.Values{}
	query.Set("container", "main")
	query.Set("timestamps", "true")
	if follow {
		query.Set("follow", "true")
	}
	if !since.IsZero() {
		query.Set("sinceTime", since.Add(time.Nanosecond).Format(time.RFC3339Nano))
	}

	req, err := c.newRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/namespaces/%s/pods/%s/log?%s", c.namespace, podName, query.Encode()), nil)
	if err != nil {
		return since, err
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return since, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return since, fmt.Errorf("kubernetes logs request failed: %s: %s", resp.Status, string(data))
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lastTimestamp := since
	for scanner.Scan() {
		timestampText, message, found := strings.Cut(scanner.Text(), " ")
		if !found {
			continue
		}
		timestamp, err := time.Parse(time.RFC3339Nano, timestampText)
		if err != nil || !timestamp.After(lastTimestamp) {
			continue
		}
		if _, err := file.WriteString(message + "\n"); err != nil {
			return lastTimestamp, err
		}
		lastTimestamp = timestamp
	}
	return lastTimestamp, scanner.Err()
}

func (c *kubernetesClient) deleteJob(ctx context.Context, name string) error {
	path := fmt.Sprintf("/apis/batch/v1/namespaces/%s/jobs/%s?propagationPolicy=Background", c.namespace, name)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *kubernetesClient) deletePodDisruptionBudget(ctx context.Context, name string) error {
	path := fmt.Sprintf("/apis/policy/v1/namespaces/%s/poddisruptionbudgets/%s", c.namespace, name)
	return c.doJSON(ctx, http.MethodDelete, path, nil, nil)
}

func (c *kubernetesClient) cleanupPodDisruptionBudget(name string) {
	cleanupContext, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()
	if err := c.deletePodDisruptionBudget(cleanupContext, name); err != nil && !isKubernetesStatus(err, http.StatusNotFound) {
		log.Warn().Err(err).Str("job", name).Msg("Could not delete Job PodDisruptionBudget")
	}
}

func (c *kubernetesClient) doJSON(ctx context.Context, method string, path string, body any, out any) error {
	var bodyData []byte
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyData = data
	}

	retryInterval := c.apiRetryInterval
	if retryInterval <= 0 {
		retryInterval = 500 * time.Millisecond
	}
	maxRetryInterval := c.apiRetryMaxInterval
	if maxRetryInterval <= 0 {
		maxRetryInterval = 10 * time.Second
	}

	for {
		var reader io.Reader
		if bodyData != nil {
			reader = bytes.NewReader(bodyData)
		}
		req, requestBuildErr := c.newRequest(ctx, method, path, reader)
		if requestBuildErr != nil {
			return requestBuildErr
		}
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, requestErr := c.http.Do(req)
		if requestErr == nil && resp.StatusCode >= 200 && resp.StatusCode < 300 {
			if out == nil {
				resp.Body.Close()
				return nil
			}
			decodeErr := json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			return decodeErr
		}

		var err error
		retryable := requestErr != nil
		if requestErr != nil {
			err = requestErr
		} else {
			data, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
			resp.Body.Close()
			err = &kubernetesAPIError{statusCode: resp.StatusCode, message: fmt.Sprintf("kubernetes request failed: %s %s: %s: %s", method, path, resp.Status, string(data))}
			retryable = resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500
		}
		if !retryable {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.Warn().Err(err).Str("method", method).Str("path", path).Dur("retryIn", retryInterval).Msg("Retrying Kubernetes API request")
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		if retryInterval < maxRetryInterval {
			retryInterval *= 2
			if retryInterval > maxRetryInterval {
				retryInterval = maxRetryInterval
			}
		}
	}
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

func jobOutcome(jobName string, job *kubernetesJob) (bool, int, error) {
	if job.Status.Succeeded > 0 {
		return true, 0, nil
	}
	for _, condition := range job.Status.Conditions {
		if condition.Status != "True" {
			continue
		}
		switch condition.Type {
		case "Complete":
			return true, 0, nil
		case "Failed":
			if condition.Message != "" {
				return true, 1, errors.New(condition.Message)
			}
			if condition.Reason != "" {
				return true, 1, fmt.Errorf("job %s failed: %s", jobName, condition.Reason)
			}
			return true, 1, fmt.Errorf("job %s failed", jobName)
		}
	}
	return false, 0, nil
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
		CreationTimestamp string `json:"creationTimestamp"`
		DeletionTimestamp string `json:"deletionTimestamp"`
	} `json:"metadata"`
	Spec struct {
		NodeName string `json:"nodeName"`
	} `json:"spec"`
	Status struct {
		Phase             string `json:"phase"`
		StartTime         string `json:"startTime"`
		ContainerStatuses []struct {
			Name  string `json:"name"`
			State struct {
				Waiting *struct {
					Reason  string `json:"reason"`
					Message string `json:"message"`
				} `json:"waiting"`
				Terminated *struct {
					ExitCode   int    `json:"exitCode"`
					Reason     string `json:"reason"`
					Message    string `json:"message"`
					StartedAt  string `json:"startedAt"`
					FinishedAt string `json:"finishedAt"`
				} `json:"terminated"`
			} `json:"state"`
		} `json:"containerStatuses"`
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

func (p kubernetesPod) observation() PodObservation {
	observation := PodObservation{
		PodName:   p.Metadata.Name,
		NodeName:  p.Spec.NodeName,
		Status:    p.status(),
		CreatedAt: parseKubernetesTime(p.Metadata.CreationTimestamp),
		StartedAt: parseKubernetesTime(p.Status.StartTime),
	}
	for _, container := range p.Status.ContainerStatuses {
		if container.Name != "main" {
			continue
		}
		if container.State.Terminated != nil {
			terminated := container.State.Terminated
			exitCode := terminated.ExitCode
			observation.ExitCode = &exitCode
			observation.Reason = terminated.Reason
			observation.Message = terminated.Message
			if startedAt := parseKubernetesTime(terminated.StartedAt); startedAt != nil {
				observation.StartedAt = startedAt
			}
			observation.FinishedAt = parseKubernetesTime(terminated.FinishedAt)
		} else if container.State.Waiting != nil {
			observation.Reason = container.State.Waiting.Reason
			observation.Message = container.State.Waiting.Message
		}
		break
	}
	return observation
}

func parseKubernetesTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}

func sortPods(pods []kubernetesPod) {
	sort.SliceStable(pods, func(left, right int) bool {
		if pods[left].Metadata.CreationTimestamp == pods[right].Metadata.CreationTimestamp {
			return pods[left].Metadata.Name < pods[right].Metadata.Name
		}
		return pods[left].Metadata.CreationTimestamp < pods[right].Metadata.CreationTimestamp
	})
}
