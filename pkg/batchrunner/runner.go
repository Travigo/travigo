package batchrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
)

const smartPreparationBatchSize = 10

type Runner struct {
	config   Config
	store    *Store
	executor TaskExecutor

	mu     sync.Mutex
	active map[string]*activeRun

	databaseMu      sync.Mutex
	databaseReady   bool
	databaseConnect func() error
	canPrepare      func(*datasets.DataSet) bool
	prepareSource   func(context.Context, *datasets.DataSet, bool, *os.File) (bool, string, error)
	datasetsOnce    sync.Once
	datasetsByID    map[string]datasets.DataSet
}

type activeRun struct {
	run    *Run
	mu     sync.Mutex
	cancel context.CancelFunc
}

func NewRunner(config Config, store *Store, executor TaskExecutor) *Runner {
	return &Runner{
		config:          config,
		store:           store,
		executor:        executor,
		active:          map[string]*activeRun{},
		databaseConnect: database.Connect,
		canPrepare:      manager.CanPrepareDatasetSource,
		prepareSource:   manager.PrepareDatasetSource,
	}
}

func (r *Runner) StartRun(options RunOptions) (*Run, error) {
	options = defaultRunOptions(options)
	if options.SmartMode && r.config.ArtifactBaseURL == "" {
		return nil, errors.New("smart mode requires an internal artifact URL")
	}
	runID := newRunID()

	r.mu.Lock()
	if !r.config.AllowConcurrentRuns && len(r.active) > 0 {
		r.mu.Unlock()
		return nil, errors.New("a batch run is already active")
	}
	r.active[runID] = nil
	r.mu.Unlock()

	plan := BuildPlan()
	run := &Run{
		ID:        runID,
		Status:    RunStatusPending,
		Options:   options,
		Tasks:     BuildRunTasks(plan, options),
		CreatedAt: time.Now().UTC(),
	}
	for i := range run.Tasks {
		path, err := r.store.PrepareLog(run.ID, run.Tasks[i].ID)
		if err != nil {
			r.clearActive(run.ID)
			return nil, err
		}
		run.Tasks[i].LogPath = path
	}

	if len(run.Tasks) == 0 {
		r.clearActive(run.ID)
		return nil, errors.New("no tasks selected")
	}

	if err := r.store.SaveRun(run); err != nil {
		r.clearActive(run.ID)
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	active := &activeRun{run: run, cancel: cancel}
	response := *run
	response.Tasks = append([]Task(nil), run.Tasks...)
	r.mu.Lock()
	r.active[run.ID] = active
	r.mu.Unlock()
	go r.executeRun(ctx, active)

	return &response, nil
}

// ResumeRuns reconnects the runner to unfinished work recorded on persistent storage.
// Kubernetes Jobs outlive this process, so running tasks attach to their existing Job.
func (r *Runner) ResumeRuns() error {
	runs, err := r.store.ListRuns()
	if err != nil {
		return err
	}

	for i := range runs {
		run := &runs[i]
		if run.Status != RunStatusPending && run.Status != RunStatusRunning {
			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		r.mu.Lock()
		if _, exists := r.active[run.ID]; exists {
			r.mu.Unlock()
			cancel()
			continue
		}
		active := &activeRun{run: run, cancel: cancel}
		r.active[run.ID] = active
		if run.CancelRequested {
			cancel()
		}
		r.mu.Unlock()
		go r.executeRun(ctx, active)
	}

	return nil
}

func (r *Runner) CancelRun(id string) error {
	r.mu.Lock()
	active := r.active[id]
	r.mu.Unlock()
	if active == nil {
		run, err := r.store.LoadRun(id)
		if err != nil {
			return err
		}
		if isTerminalRunStatus(run.Status) {
			return nil
		}
		return errors.New("batch run is not active")
	}

	active.mu.Lock()
	defer active.mu.Unlock()
	run := active.run
	if isTerminalRunStatus(run.Status) || run.CancelRequested {
		return nil
	}
	run.CancelRequested = true
	if err := r.store.SaveRun(run); err != nil {
		run.CancelRequested = false
		return fmt.Errorf("persist cancellation request: %w", err)
	}
	active.cancel()
	return nil
}

func (r *Runner) clearActive(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, id)
}

func (r *Runner) executeRun(ctx context.Context, active *activeRun) {
	run, runMu := active.run, &active.mu
	defer active.cancel()
	defer r.clearActive(run.ID)

	runMu.Lock()
	if run.StartedAt == nil {
		started := time.Now().UTC()
		run.StartedAt = &started
	}
	run.Status = RunStatusRunning
	run.Error = ""
	_ = r.store.SaveRun(run)
	runMu.Unlock()

	failed := false
	for _, stage := range buildStages(run.Tasks) {
		if ctx.Err() != nil {
			break
		}
		if failed && !run.Options.ContinueOnFailure {
			r.markStageSkipped(run, runMu, stage)
			continue
		}
		if r.executeStage(ctx, run, runMu, stage) {
			failed = true
		}
	}

	jobsStopped := false
	for {
		runMu.Lock()
		if run.CancelRequested && !jobsStopped {
			runMu.Unlock()
			r.stopRunJobs(run)
			jobsStopped = true
			continue
		}

		finished := time.Now().UTC()
		run.FinishedAt = &finished
		switch {
		case run.CancelRequested:
			run.Status = RunStatusCancelled
			for i := range run.Tasks {
				task := &run.Tasks[i]
				switch task.Status {
				case TaskStatusPending, TaskStatusChecking, TaskStatusRunning:
					task.Status = TaskStatusCancelled
					task.FinishedAt = &finished
					task.Error = "run cancelled"
				}
			}
		case failed:
			run.Status = RunStatusFailed
		default:
			run.Status = RunStatusSucceeded
		}
		err := r.store.SaveRun(run)
		if err != nil {
			run.Status = RunStatusRunning
			run.FinishedAt = nil
		}
		runMu.Unlock()
		if err == nil {
			break
		}
		log.Error().Err(err).Str("run", run.ID).Msg("Unable to save terminal run state; retrying")
		time.Sleep(2 * time.Second)
	}

	if err := r.store.RemoveRunArtifacts(run.ID); err != nil {
		log.Warn().Err(err).Str("run", run.ID).Msg("Unable to clean completed run artifacts")
	}
}

func (r *Runner) stopRunJobs(run *Run) {
	var jobs []string
	for _, task := range run.Tasks {
		if task.Status == TaskStatusRunning && task.JobName != "" {
			jobs = append(jobs, task.JobName)
		}
	}
	for len(jobs) > 0 {
		remaining := jobs[:0]
		for _, job := range jobs {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			err := r.executor.DeleteJob(ctx, job)
			cancel()
			if err != nil {
				log.Warn().Err(err).Str("job", job).Msg("Unable to confirm Job cancellation; retrying")
				remaining = append(remaining, job)
			}
		}
		jobs = remaining
		if len(jobs) > 0 {
			time.Sleep(2 * time.Second)
		}
	}
}

func (r *Runner) executeStage(ctx context.Context, run *Run, runMu *sync.Mutex, indexes []int) bool {
	if len(indexes) == 0 {
		return false
	}
	if run.Options.SmartMode && r.config.ArtifactBaseURL != "" && run.Tasks[indexes[0]].Kind == TaskKindDataset {
		return r.executeSmartStage(ctx, run, runMu, indexes)
	}

	failed := false
	ready := make([]int, 0, len(indexes))
	for _, index := range indexes {
		switch run.Tasks[index].Status {
		case TaskStatusSucceeded, TaskStatusSkipped, TaskStatusCancelled:
			continue
		case TaskStatusFailed:
			failed = true
			continue
		}
		ready = append(ready, index)
	}
	return r.executeIndexes(ctx, run, runMu, ready) || failed
}

// executeSmartStage preflights dataset sources in bounded waves. Each wave
// checks at most ten sources at once; only changed sources and legacy fallbacks
// become child jobs, which use the Smart mode limit for their stage.
func (r *Runner) executeSmartStage(ctx context.Context, run *Run, runMu *sync.Mutex, indexes []int) bool {
	type preparation struct {
		index   int
		dataset datasets.DataSet
		err     error
	}
	type result struct {
		index int
		token string
		err   error
	}
	failed := false
	databaseAttempted := false
	var databaseErr error
	for batch := range slices.Chunk(indexes, smartPreparationBatchSize) {
		if ctx.Err() != nil {
			break
		}
		ready := make([]int, 0, len(batch))
		preparations := make([]preparation, 0, len(batch))
		for _, index := range batch {
			task := &run.Tasks[index]
			switch task.Status {
			case TaskStatusSucceeded, TaskStatusSkipped, TaskStatusCancelled:
				continue
			case TaskStatusFailed:
				failed = true
				continue
			}
			if task.ArtifactToken != "" {
				keep, err := r.keepStagedTask(ctx, run.ID, task)
				if err != nil {
					if ctx.Err() != nil {
						break
					}
					preparations = append(preparations, preparation{index: index, err: err})
					continue
				}
				if keep {
					ready = append(ready, index)
					continue
				}
				runMu.Lock()
				clearStagedSource(task)
				runMu.Unlock()
			}
			if task.Status == TaskStatusRunning {
				ready = append(ready, index)
				continue
			}
			dataset, ok := r.dataset(task.DatasetID)
			if !ok || !r.canPrepare(&dataset) {
				ready = append(ready, index)
				continue
			}
			preparations = append(preparations, preparation{index: index, dataset: dataset})
		}
		if ctx.Err() != nil {
			break
		}
		if len(preparations) > 0 {
			runMu.Lock()
			for _, prep := range preparations {
				run.Tasks[prep.index].Status = TaskStatusChecking
			}
			if err := r.store.SaveRun(run); err != nil {
				log.Warn().Err(err).Str("run", run.ID).Msg("Unable to save source checking progress")
			}
			runMu.Unlock()
			if !databaseAttempted {
				databaseAttempted = true
				databaseErr = r.connectDatabase()
			}
		}

		results := make(chan result, len(preparations))
		for _, prep := range preparations {
			switch {
			case prep.err != nil:
				results <- result{index: prep.index, err: prep.err}
			case databaseErr != nil:
				results <- result{index: prep.index, err: fmt.Errorf("connect to database: %w", databaseErr)}
			default:
				go func() {
					token, err := r.store.StageArtifact(run.ID, func(file *os.File) (bool, string, error) {
						return r.prepareSource(ctx, &prep.dataset, run.Options.ForceImport, file)
					})
					results <- result{index: prep.index, token: token, err: err}
				}()
			}
		}
		for range preparations {
			result := <-results
			if ctx.Err() != nil {
				continue
			}
			runMu.Lock()
			task := &run.Tasks[result.index]
			switch {
			case result.err != nil:
				r.finishPreparation(run, task, TaskStatusFailed, "source preparation failed: "+result.err.Error())
				failed = true
			case result.token == "":
				r.finishPreparation(run, task, TaskStatusSkipped, "dataset source is unchanged; import skipped")
			default:
				task.Status = TaskStatusPending
				task.ArtifactToken = result.token
				source := strings.TrimRight(r.config.ArtifactBaseURL, "/") + "/artifacts/" + run.ID + "/" + result.token
				task.Args = append(task.Args, "--source-override", source)
				if err := r.store.SaveRun(run); err != nil {
					_ = r.store.RemoveArtifact(run.ID, result.token)
					clearStagedSource(task)
					r.finishPreparation(run, task, TaskStatusFailed, "persist staged source: "+err.Error())
					failed = true
				} else {
					ready = append(ready, result.index)
				}
			}
			runMu.Unlock()
		}
		if r.executeIndexes(ctx, run, runMu, ready) {
			failed = true
		}
	}
	return failed
}

func (r *Runner) keepStagedTask(ctx context.Context, runID string, task *Task) (bool, error) {
	exists, err := r.store.artifactExists(runID, task.ArtifactToken)
	if err != nil || exists || task.Status != TaskStatusRunning {
		return exists, err
	}
	// An existing Job still has its original URL and may already have fetched the feed.
	// Only check the publisher again if the old Job is absent.
	for {
		exists, err := r.executor.JobExists(ctx, task.JobName)
		if err == nil {
			return exists, nil
		}
		log.Warn().Err(err).Str("job", task.JobName).Msg("Unable to check Job before restaging; retrying")
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func clearStagedSource(task *Task) {
	task.ArtifactToken = ""
	if i := slices.Index(task.Args, "--source-override"); i >= 0 {
		task.Args = slices.Delete(task.Args, i, min(i+2, len(task.Args)))
	}
	task.Status = TaskStatusPending
	task.JobName = ""
	task.PodStatus = ""
}

func stageConcurrency(options RunOptions, task Task) int {
	if !options.SmartMode {
		return max(1, options.MaxActiveTasks)
	}
	if task.Kind == TaskKindDataset {
		switch task.Size {
		case "", "small":
			return 3
		case "medium":
			return 2
		}
	}
	return 1
}

func (r *Runner) executeIndexes(ctx context.Context, run *Run, runMu *sync.Mutex, indexes []int) bool {
	if len(indexes) == 0 {
		return false
	}
	maxActive := min(len(indexes), stageConcurrency(run.Options, run.Tasks[indexes[0]]))
	jobs := make(chan int)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	failed := false
	worker := func() {
		defer wg.Done()
		for index := range jobs {
			if ctx.Err() != nil {
				continue
			}
			if r.executeTask(ctx, run, runMu, index) {
				failedMu.Lock()
				failed = true
				failedMu.Unlock()
			}
		}
	}
	for i := 0; i < maxActive; i++ {
		wg.Add(1)
		go worker()
	}
	for _, index := range indexes {
		jobs <- index
	}
	close(jobs)
	wg.Wait()
	return failed
}

func (r *Runner) dataset(identifier string) (datasets.DataSet, bool) {
	r.datasetsOnce.Do(func() {
		r.datasetsByID = make(map[string]datasets.DataSet)
		for _, dataset := range manager.GetRegisteredDataSets() {
			r.datasetsByID[dataset.Identifier] = dataset
		}
	})
	dataset, ok := r.datasetsByID[identifier]
	return dataset, ok
}

func (r *Runner) connectDatabase() error {
	r.databaseMu.Lock()
	defer r.databaseMu.Unlock()
	if r.databaseReady {
		return nil
	}
	if r.databaseConnect == nil {
		return nil
	}
	if err := r.databaseConnect(); err != nil {
		return err
	}
	r.databaseReady = true
	return nil
}

func (r *Runner) finishPreparation(run *Run, task *Task, status TaskStatus, message string) {
	now := time.Now().UTC()
	task.Status = status
	task.FinishedAt = &now
	task.Error = message
	_ = r.store.SaveRun(run)
	writeTaskReason(task.LogPath, message)
}

func writeTaskReason(path, message string) {
	if path == "" {
		return
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(file, message)
	_ = file.Close()
}

func (r *Runner) executeTask(ctx context.Context, run *Run, runMu *sync.Mutex, index int) bool {
	now := time.Now().UTC()
	runMu.Lock()
	if run.CancelRequested || ctx.Err() != nil {
		runMu.Unlock()
		return false
	}
	recovering := run.Tasks[index].Status == TaskStatusRunning
	if run.Tasks[index].JobName == "" {
		run.Tasks[index].JobName = jobNameForTask(run.ID, run.Tasks[index].ID)
	}
	run.Tasks[index].Status = TaskStatusRunning
	if !recovering {
		run.Tasks[index].PodStatus = PodStatusPending
	}
	if run.Tasks[index].StartedAt == nil {
		run.Tasks[index].StartedAt = &now
	}
	run.Tasks[index].Error = ""
	task := run.Tasks[index]
	if err := r.store.SaveRun(run); err != nil && !recovering {
		r.finishPreparation(run, &run.Tasks[index], TaskStatusFailed, "persist job start: "+err.Error())
		runMu.Unlock()
		return true
	}
	runMu.Unlock()

	updatePod := func(observation PodObservation) {
		runMu.Lock()
		defer runMu.Unlock()
		if run.CancelRequested || ctx.Err() != nil || run.Tasks[index].Status != TaskStatusRunning {
			return
		}
		changed := false
		if observation.Status != "" && run.Tasks[index].PodStatus != observation.Status {
			run.Tasks[index].PodStatus = observation.Status
			changed = true
		}
		if observation.PodName != "" {
			changed = mergeTaskAttempt(&run.Tasks[index], observation) || changed
		}
		if changed {
			_ = r.store.SaveRun(run)
		}
	}
	exitCode, err := r.executor.RunTask(ctx, run.ID, &task, task.LogPath, recovering, updatePod)

	finished := time.Now().UTC()
	runMu.Lock()
	run.Tasks[index].JobName = task.JobName
	run.Tasks[index].FinishedAt = &finished
	run.Tasks[index].ExitCode = &exitCode
	if run.CancelRequested || ctx.Err() != nil {
		run.Tasks[index].PodStatus = PodStatusTerminating
		runMu.Unlock()
		return false
	}
	if err != nil || exitCode != 0 {
		run.Tasks[index].Status = TaskStatusFailed
		run.Tasks[index].PodStatus = PodStatusFailed
		if err != nil {
			run.Tasks[index].Error = err.Error()
		} else {
			run.Tasks[index].Error = fmt.Sprintf("job exited with code %d", exitCode)
		}
	} else {
		run.Tasks[index].Status = TaskStatusSucceeded
		run.Tasks[index].PodStatus = PodStatusSucceeded
	}
	run.Tasks[index].ArtifactToken = ""
	saveErr := r.store.SaveRun(run)
	if saveErr != nil {
		run.Tasks[index].ArtifactToken = task.ArtifactToken
	}
	runMu.Unlock()
	if saveErr != nil {
		log.Error().Err(saveErr).Str("run", run.ID).Str("task", task.ID).Msg("Unable to save terminal task state")
	} else if task.ArtifactToken != "" {
		if err := r.store.RemoveArtifact(run.ID, task.ArtifactToken); err != nil {
			log.Warn().Err(err).Str("run", run.ID).Str("task", task.ID).Msg("Unable to remove terminal task artifact")
		}
	}

	return err != nil || exitCode != 0
}

func mergeTaskAttempt(task *Task, observation PodObservation) bool {
	for index := range task.Attempts {
		if task.Attempts[index].PodName != observation.PodName {
			continue
		}
		return updateTaskAttempt(&task.Attempts[index], observation)
	}

	attempt := TaskAttempt{
		Attempt: len(task.Attempts) + 1,
		PodName: observation.PodName,
	}
	updateTaskAttempt(&attempt, observation)
	task.Attempts = append(task.Attempts, attempt)
	return true
}

func updateTaskAttempt(attempt *TaskAttempt, observation PodObservation) bool {
	changed := false
	if observation.NodeName != "" && attempt.NodeName != observation.NodeName {
		attempt.NodeName = observation.NodeName
		changed = true
	}
	if observation.Status != "" && attempt.Status != observation.Status {
		attempt.Status = observation.Status
		changed = true
	}
	if observation.CreatedAt != nil && !timesEqual(attempt.CreatedAt, observation.CreatedAt) {
		attempt.CreatedAt = observation.CreatedAt
		changed = true
	}
	if observation.StartedAt != nil && !timesEqual(attempt.StartedAt, observation.StartedAt) {
		attempt.StartedAt = observation.StartedAt
		changed = true
	}
	if observation.FinishedAt != nil && !timesEqual(attempt.FinishedAt, observation.FinishedAt) {
		attempt.FinishedAt = observation.FinishedAt
		changed = true
	}
	if observation.ExitCode != nil && (attempt.ExitCode == nil || *attempt.ExitCode != *observation.ExitCode) {
		attempt.ExitCode = observation.ExitCode
		changed = true
	}
	if attempt.Reason != observation.Reason {
		attempt.Reason = observation.Reason
		changed = true
	}
	if attempt.Message != observation.Message {
		attempt.Message = observation.Message
		changed = true
	}
	return changed
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Equal(*right)
}

func (r *Runner) markStageSkipped(run *Run, runMu *sync.Mutex, indexes []int) {
	runMu.Lock()
	defer runMu.Unlock()
	now := time.Now().UTC()
	for _, index := range indexes {
		if run.Tasks[index].Status != TaskStatusPending && run.Tasks[index].Status != TaskStatusChecking {
			continue
		}
		run.Tasks[index].Status = TaskStatusSkipped
		run.Tasks[index].FinishedAt = &now
		run.Tasks[index].Error = "skipped after prior failure"
	}
	_ = r.store.SaveRun(run)
}

func buildStages(tasks []Task) [][]int {
	sizeIndexes := map[string][]int{
		"small":  {},
		"medium": {},
		"large":  {},
	}
	for i, task := range tasks {
		if task.Kind == TaskKindDataset && slices.Contains(initialDatasetSizes, task.Size) {
			sizeIndexes[task.Size] = append(sizeIndexes[task.Size], i)
		}
	}

	ordered := [][]int{}
	for _, size := range initialDatasetSizes {
		if len(sizeIndexes[size]) > 0 {
			ordered = append(ordered, sizeIndexes[size])
		}
	}
	for index := 0; index < len(tasks); {
		task := tasks[index]
		if task.Kind == TaskKindDataset && slices.Contains(initialDatasetSizes, task.Size) {
			index++
			continue
		}
		if task.Kind != TaskKindDataset {
			ordered = append(ordered, []int{index})
			index++
			continue
		}

		stageSize := task.Size
		stage := []int{}
		for index < len(tasks) && tasks[index].Kind == TaskKindDataset && tasks[index].Size == stageSize {
			stage = append(stage, index)
			index++
		}
		ordered = append(ordered, stage)
	}
	return ordered
}

func newRunID() string {
	return time.Now().UTC().Format("20060102-150405-000000000")
}
