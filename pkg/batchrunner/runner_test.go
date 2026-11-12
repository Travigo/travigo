package batchrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

type recordingExecutor struct {
	tasks      chan Task
	recovering chan bool
}

func (e *recordingExecutor) RunTask(_ context.Context, _ string, task *Task, _ string, recovering bool, updatePod func(PodObservation)) (int, error) {
	e.tasks <- *task
	e.recovering <- recovering
	updatePod(PodObservation{PodName: "running-pod", NodeName: "batch-node", Status: PodStatusRunning})
	return 0, nil
}

func (e *recordingExecutor) JobExists(context.Context, string) (bool, error) {
	return false, nil
}

func (e *recordingExecutor) DeleteJob(context.Context, string) error {
	return nil
}

func TestResumeRunsReconnectsPersistedRunningTask(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	started := time.Now().UTC().Add(-time.Minute)
	run := &Run{
		ID:        "20260711-120000-000000000",
		Status:    RunStatusRunning,
		CreatedAt: started,
		StartedAt: &started,
		Tasks: []Task{{
			ID:        "link-stops",
			Kind:      TaskKindLinkStops,
			Status:    TaskStatusRunning,
			JobName:   "travigo-batch-20260711-120000-000000000-link-stops",
			StartedAt: &started,
		}},
	}
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}

	executor := &recordingExecutor{tasks: make(chan Task, 1), recovering: make(chan bool, 1)}
	runner := NewRunner(Config{}, store, executor)
	if err := runner.ResumeRuns(); err != nil {
		t.Fatal(err)
	}

	select {
	case task := <-executor.tasks:
		if task.JobName != run.Tasks[0].JobName {
			t.Fatalf("recovered task job name = %q, want %q", task.JobName, run.Tasks[0].JobName)
		}
	case <-time.After(time.Second):
		t.Fatal("recovered task was not started")
	}
	if !<-executor.recovering {
		t.Fatal("recovered task was not marked as recovering")
	}

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		resumed, err := store.LoadRun(run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if resumed.Status == RunStatusSucceeded && resumed.Tasks[0].Status == TaskStatusSucceeded {
			if !resumed.StartedAt.Equal(started) {
				t.Fatalf("recovered run start time = %v, want %v", resumed.StartedAt, started)
			}
			if resumed.Tasks[0].PodStatus != PodStatusSucceeded {
				t.Fatalf("recovered task pod status = %q, want succeeded", resumed.Tasks[0].PodStatus)
			}
			if !resumed.Tasks[0].StartedAt.Equal(started) {
				t.Fatalf("recovered task start time = %v, want %v", resumed.Tasks[0].StartedAt, started)
			}
			if len(resumed.Tasks[0].Attempts) != 1 || resumed.Tasks[0].Attempts[0].PodName != "running-pod" || resumed.Tasks[0].Attempts[0].NodeName != "batch-node" {
				t.Fatalf("recovered task attempts = %#v", resumed.Tasks[0].Attempts)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("recovered run did not complete")
}

func TestSmartStageUsesStagedSourceForChangedDataset(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{tasks: make(chan Task, 1), recovering: make(chan bool, 1)}
	runner := NewRunner(Config{ArtifactBaseURL: "http://runner:8081/"}, store, executor)
	runner.datasetsOnce.Do(func() { runner.datasetsByID = map[string]datasets.DataSet{"feed": {Identifier: "feed"}} })
	runner.canPrepare = func(*datasets.DataSet) bool { return true }
	runner.databaseConnect = func() error { return nil }
	runner.prepareSource = func(_ context.Context, _ *datasets.DataSet, _ bool, file *os.File) (bool, string, error) {
		_, _ = file.WriteString("feed data")
		return true, `"v2"`, nil
	}
	task := Task{ID: "feed", DatasetID: "feed", Kind: TaskKindDataset, Status: TaskStatusPending, Args: []string{"data-importer", "dataset", "--id", "feed"}}
	run := &Run{ID: "20261111-120000-000000000", Status: RunStatusRunning, Options: RunOptions{SmartMode: true, MaxActiveTasks: 1}, Tasks: []Task{task}}
	if failed := runner.executeSmartStage(context.Background(), run, &sync.Mutex{}, []int{0}); failed {
		t.Fatal("smart stage failed")
	}
	started := <-executor.tasks
	if len(started.Args) != 6 || started.Args[4] != "--source-override" || !strings.HasPrefix(started.Args[5], "http://runner:8081/artifacts/") {
		t.Fatalf("executor args = %v, want staged source override", started.Args)
	}
	if run.Tasks[0].ArtifactToken != "" {
		t.Fatalf("successful task retained artifact token %q", run.Tasks[0].ArtifactToken)
	}
	if run.Tasks[0].Status != TaskStatusSucceeded {
		t.Fatalf("task status = %q", run.Tasks[0].Status)
	}
}

func TestSmartStageSkipsUnchangedAndRunsLocalDatasetDirectly(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{tasks: make(chan Task, 2), recovering: make(chan bool, 2)}
	runner := NewRunner(Config{ArtifactBaseURL: "http://runner:8081"}, store, executor)
	runner.datasetsOnce.Do(func() {
		runner.datasetsByID = map[string]datasets.DataSet{
			"unchanged": {Identifier: "unchanged", Format: datasets.DataSetFormatGTFSSchedule, Source: "https://example.test/gtfs.zip"},
			"local":     {Identifier: "local", Format: datasets.DataSetFormatGTFSSchedule, Source: "/data/gtfs.zip"},
		}
	})
	runner.databaseConnect = func() error { return nil }
	runner.prepareSource = func(_ context.Context, dataset *datasets.DataSet, _ bool, _ *os.File) (bool, string, error) {
		if dataset.Identifier != "unchanged" {
			return false, "", fmt.Errorf("unexpected source preparation for %s", dataset.Identifier)
		}
		return false, "", nil
	}
	run := &Run{ID: "20261111-120001-000000000", Options: RunOptions{SmartMode: true, MaxActiveTasks: 1}, Tasks: []Task{
		{ID: "unchanged", DatasetID: "unchanged", Kind: TaskKindDataset, Status: TaskStatusPending, Args: []string{"import", "--id", "unchanged"}},
		{ID: "local", DatasetID: "local", Kind: TaskKindDataset, Status: TaskStatusPending, Args: []string{"import", "--id", "local"}},
	}}
	if failed := runner.executeSmartStage(context.Background(), run, &sync.Mutex{}, []int{0, 1}); failed {
		t.Fatal("smart stage failed")
	}
	if run.Tasks[0].Status != TaskStatusSkipped || !strings.Contains(run.Tasks[0].Error, "unchanged") {
		t.Fatalf("unchanged task = %#v", run.Tasks[0])
	}
	started := <-executor.tasks
	if started.ID != "local" || len(started.Args) != 3 {
		t.Fatalf("local task should use legacy args, got %#v", started)
	}
	if run.Tasks[1].Status != TaskStatusSucceeded {
		t.Fatalf("fallback task status = %q", run.Tasks[1].Status)
	}
}

func TestSmartPreparationFailureIsTerminalAndDoesNotCreateJob(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{tasks: make(chan Task, 1), recovering: make(chan bool, 1)}
	runner := NewRunner(Config{ArtifactBaseURL: "http://runner:8081"}, store, executor)
	runner.datasetsOnce.Do(func() { runner.datasetsByID = map[string]datasets.DataSet{"bad": {Identifier: "bad"}} })
	runner.canPrepare = func(*datasets.DataSet) bool { return true }
	runner.databaseConnect = func() error { return nil }
	runner.prepareSource = func(context.Context, *datasets.DataSet, bool, *os.File) (bool, string, error) {
		return false, "", errors.New("publisher unavailable")
	}
	run := &Run{ID: "20261111-120002-000000000", Options: RunOptions{SmartMode: true}, Tasks: []Task{{ID: "bad", DatasetID: "bad", Kind: TaskKindDataset, Status: TaskStatusPending}}}
	if !runner.executeSmartStage(context.Background(), run, &sync.Mutex{}, []int{0}) {
		t.Fatal("expected failed stage")
	}
	if run.Tasks[0].Status != TaskStatusFailed || !strings.Contains(run.Tasks[0].Error, "publisher unavailable") {
		t.Fatalf("failed task = %#v", run.Tasks[0])
	}
	select {
	case <-executor.tasks:
		t.Fatal("failed check created a child job")
	default:
	}
}

func TestStageConcurrency(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options RunOptions
		task    Task
		want    int
	}{
		{"small", RunOptions{SmartMode: true, MaxActiveTasks: 1}, Task{Kind: TaskKindDataset, Size: "small"}, 3},
		{"medium", RunOptions{SmartMode: true, MaxActiveTasks: 4}, Task{Kind: TaskKindDataset, Size: "medium"}, 2},
		{"large", RunOptions{SmartMode: true, MaxActiveTasks: 4}, Task{Kind: TaskKindDataset, Size: "large"}, 1},
		{"enrichment dataset", RunOptions{SmartMode: true}, Task{Kind: TaskKindDataset, Size: journeyEnrichmentGroup}, 1},
		{"post-processing", RunOptions{SmartMode: true, MaxActiveTasks: 4}, Task{Kind: TaskKindLinkStops}, 1},
		{"regular mode", RunOptions{MaxActiveTasks: 4}, Task{Kind: TaskKindDataset, Size: "large"}, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stageConcurrency(tc.options, tc.task); got != tc.want {
				t.Errorf("stage concurrency = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSmartPreparationUsesBoundedBatches(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	taskCount := smartPreparationBatchSize + 2
	executor := &recordingExecutor{tasks: make(chan Task, taskCount), recovering: make(chan bool, taskCount)}
	runner := NewRunner(Config{ArtifactBaseURL: "http://runner:8081"}, store, executor)
	datasetsByID := map[string]datasets.DataSet{}
	tasks := make([]Task, taskCount)
	indexes := make([]int, taskCount)
	for i := range tasks {
		indexes[i] = i
		id := fmt.Sprintf("feed-%d", i)
		datasetsByID[id] = datasets.DataSet{Identifier: id}
		tasks[i] = Task{ID: id, DatasetID: id, Kind: TaskKindDataset, Status: TaskStatusPending}
	}
	runner.datasetsOnce.Do(func() { runner.datasetsByID = datasetsByID })
	runner.canPrepare = func(*datasets.DataSet) bool { return true }
	runner.databaseConnect = func() error { return nil }
	started := make(chan struct{}, len(tasks))
	release := make(chan struct{})
	runner.prepareSource = func(ctx context.Context, _ *datasets.DataSet, _ bool, _ *os.File) (bool, string, error) {
		started <- struct{}{}
		select {
		case <-release:
			return false, "", nil
		case <-ctx.Done():
			return false, "", ctx.Err()
		}
	}
	run := &Run{ID: "20261111-120003-000000000", Options: RunOptions{SmartMode: true}, Tasks: tasks}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan bool, 1)
	go func() {
		done <- runner.executeSmartStage(ctx, run, &sync.Mutex{}, indexes)
	}()
	for range smartPreparationBatchSize {
		select {
		case <-started:
		case <-ctx.Done():
			<-done
			t.Fatalf("%d downloads did not start concurrently", smartPreparationBatchSize)
		}
	}
	visible, err := store.LoadRun(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, task := range visible.Tasks {
		want := TaskStatusPending
		if i < smartPreparationBatchSize {
			want = TaskStatusChecking
		}
		if task.Status != want || task.JobName != "" {
			t.Errorf("task %s while checking = %s, job %q, want %s and no job", task.ID, task.Status, task.JobName, want)
		}
	}
	select {
	case <-started:
		cancel()
		<-done
		t.Fatal("a download from the next batch started before the first batch finished")
	default:
	}
	close(release)
	if <-done {
		t.Fatal("smart stage failed")
	}
}

func TestResumeCancellationKeepsRunAndArtifactUntilJobStops(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{ID: "cancelled-during-restart", Status: RunStatusRunning, Tasks: []Task{{
		ID: "feed", Kind: TaskKindDataset, Status: TaskStatusRunning, JobName: "existing-job",
	}}}
	token, err := store.StageArtifact(run.ID, writeTestArtifact)
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].ArtifactToken = token
	if err := store.SaveRun(run); err != nil {
		t.Fatal(err)
	}

	// Simulate the API persisting cancellation just before the runner exits.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	beforeRestart := NewRunner(Config{}, store, nil)
	beforeRestart.active[run.ID] = &activeRun{run: run, cancel: cancel}
	if err := beforeRestart.CancelRun(run.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := store.LoadRun(run.ID)
	if err != nil || !saved.CancelRequested || ctx.Err() != context.Canceled {
		t.Fatalf("cancellation was not recorded: %+v, %v", saved, err)
	}

	attempted := make(chan string, 2)
	allowDelete := make(chan struct{})
	defer func() {
		select {
		case <-allowDelete:
		default:
			close(allowDelete)
		}
	}()
	deletionAttempts := 0
	executor := &runnerExecutorStub{
		recordingExecutor: recordingExecutor{tasks: make(chan Task, 1), recovering: make(chan bool, 1)},
		deleteJob: func(ctx context.Context, name string) error {
			deletionAttempts++
			attempted <- name
			if deletionAttempts == 1 {
				return errors.New("Kubernetes API unavailable")
			}
			select {
			case <-allowDelete:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	afterRestart := NewRunner(Config{}, store, executor)
	if err := afterRestart.ResumeRuns(); err != nil {
		t.Fatal(err)
	}
	select {
	case job := <-attempted:
		if job != "existing-job" {
			t.Errorf("deleted job %q", job)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resumed cancellation did not attempt deletion")
	}
	saved, err = store.LoadRun(run.ID)
	if err != nil || saved.Status != RunStatusRunning || !saved.CancelRequested {
		t.Fatalf("run was released before deletion was confirmed: %+v, %v", saved, err)
	}
	if exists, err := store.artifactExists(run.ID, token); err != nil || !exists {
		t.Fatalf("artifact disappeared during cancellation: %v", err)
	}
	select {
	case job := <-executor.tasks:
		t.Fatalf("cancelled task restarted: %+v", job)
	default:
	}

	// Unblock the fake Kubernetes deletion and wait for the terminal checkpoint.
	select {
	case <-attempted:
	case <-time.After(4 * time.Second):
		t.Fatal("runner did not retry deletion")
	}
	close(allowDelete)
	waitForRunStatus(t, store, run.ID, RunStatusCancelled)
}

func TestTerminalSaveFailureKeepsArtifact(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{ID: "failed-checkpoint", Tasks: []Task{{ID: "feed", Status: TaskStatusPending}}}
	token, err := store.StageArtifact(run.ID, writeTestArtifact)
	if err != nil {
		t.Fatal(err)
	}
	run.Tasks[0].ArtifactToken = token
	executor := &runnerExecutorStub{
		runTask: func() {
			if err := os.Rename(store.runsDir(), store.runsDir()+"-saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(store.runsDir(), nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	runner := NewRunner(Config{}, store, executor)
	if runner.executeTask(t.Context(), run, &sync.Mutex{}, 0) {
		t.Fatal("import itself should succeed")
	}
	if exists, err := store.artifactExists(run.ID, token); err != nil || !exists {
		t.Fatalf("artifact removed before terminal state was saved: %v", err)
	}
	if run.Tasks[0].ArtifactToken != token {
		t.Fatal("failed terminal save cleared the artifact token")
	}
}

func TestSmartStageRestagesMissingArtifactWhenJobIsAbsent(t *testing.T) {
	store, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	executor := &recordingExecutor{tasks: make(chan Task, 1), recovering: make(chan bool, 1)}
	runner := NewRunner(Config{ArtifactBaseURL: "http://runner:8081"}, store, executor)
	runner.datasetsOnce.Do(func() {
		runner.datasetsByID = map[string]datasets.DataSet{"feed": {Identifier: "feed", Format: datasets.DataSetFormatGTFSSchedule, Source: "https://example.test/feed"}}
	})
	runner.databaseConnect = func() error { return nil }
	runner.prepareSource = func(_ context.Context, _ *datasets.DataSet, _ bool, file *os.File) (bool, string, error) {
		return writeTestArtifact(file)
	}

	missingToken := strings.Repeat("a", 48)
	run := &Run{ID: "lost-artifact", Options: RunOptions{SmartMode: true}, Tasks: []Task{{
		ID: "feed", DatasetID: "feed", Kind: TaskKindDataset, Status: TaskStatusRunning, JobName: "missing-job",
		ArtifactToken: missingToken, Args: []string{"data-importer", "dataset", "--id", "feed", "--source-override", "http://runner/old"},
	}}}
	if runner.executeSmartStage(t.Context(), run, &sync.Mutex{}, []int{0}) {
		t.Fatal("restaging failed")
	}
	job := <-executor.tasks
	if len(job.Args) != 6 || job.Args[4] != "--source-override" || !strings.HasSuffix(job.Args[5], job.ArtifactToken) || job.ArtifactToken == missingToken || <-executor.recovering {
		t.Fatalf("job did not use the new artifact: %+v", job)
	}
}

type runnerExecutorStub struct {
	recordingExecutor
	runTask   func()
	deleteJob func(context.Context, string) error
}

func (e *runnerExecutorStub) RunTask(ctx context.Context, runID string, task *Task, logPath string, recovering bool, updatePod func(PodObservation)) (int, error) {
	if e.runTask != nil {
		e.runTask()
		return 0, nil
	}
	return e.recordingExecutor.RunTask(ctx, runID, task, logPath, recovering, updatePod)
}

func (e *runnerExecutorStub) DeleteJob(ctx context.Context, name string) error {
	return e.deleteJob(ctx, name)
}

func writeTestArtifact(file *os.File) (bool, string, error) {
	_, err := file.WriteString("staged content")
	return true, "", err
}

func waitForRunStatus(t *testing.T, store *Store, id string, status RunStatus) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		run, err := store.LoadRun(id)
		if err != nil {
			t.Fatal(err)
		}
		if run.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("run did not reach %s", status)
}
