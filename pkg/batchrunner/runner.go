package batchrunner

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Runner struct {
	config   Config
	store    *Store
	executor TaskExecutor

	mu     sync.Mutex
	active map[string]context.CancelFunc
}

func NewRunner(config Config, store *Store, executor TaskExecutor) *Runner {
	return &Runner{
		config:   config,
		store:    store,
		executor: executor,
		active:   map[string]context.CancelFunc{},
	}
}

func (r *Runner) StartRun(options RunOptions) (*Run, error) {
	options = defaultRunOptions(options)
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
	r.mu.Lock()
	r.active[run.ID] = cancel
	r.mu.Unlock()

	go r.executeRun(ctx, run)

	return run, nil
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
		r.active[run.ID] = cancel
		r.mu.Unlock()

		go r.executeRun(ctx, run)
	}

	return nil
}

func (r *Runner) CancelRun(id string) error {
	r.mu.Lock()
	cancel, ok := r.active[id]
	r.mu.Unlock()
	if !ok {
		run, err := r.store.LoadRun(id)
		if err != nil {
			return err
		}
		if run.Status == RunStatusRunning || run.Status == RunStatusPending {
			run.CancelRequested = true
			run.Status = RunStatusCancelled
			now := time.Now().UTC()
			run.FinishedAt = &now
			return r.store.SaveRun(run)
		}
		return nil
	}
	if cancel == nil {
		return nil
	}

	cancel()
	return nil
}

func (r *Runner) clearActive(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.active, id)
}

func (r *Runner) executeRun(ctx context.Context, run *Run) {
	var runMu sync.Mutex
	if run.StartedAt == nil {
		started := time.Now().UTC()
		run.StartedAt = &started
	}
	run.Status = RunStatusRunning
	run.Error = ""
	_ = r.store.SaveRun(run)

	failed := false
	for _, stage := range buildStages(run.Tasks) {
		if ctx.Err() != nil {
			break
		}
		if failed && !run.Options.ContinueOnFailure {
			r.markStageSkipped(run, &runMu, stage)
			continue
		}

		stageFailed := r.executeStage(ctx, run, &runMu, stage)
		if stageFailed {
			failed = true
		}
	}

	finished := time.Now().UTC()
	runMu.Lock()
	run.FinishedAt = &finished
	if ctx.Err() != nil {
		run.Status = RunStatusCancelled
		run.CancelRequested = true
		for i := range run.Tasks {
			if run.Tasks[i].Status == TaskStatusPending || run.Tasks[i].Status == TaskStatusRunning {
				run.Tasks[i].Status = TaskStatusCancelled
				now := time.Now().UTC()
				run.Tasks[i].FinishedAt = &now
				run.Tasks[i].Error = "run cancelled"
			}
		}
	} else if failed {
		run.Status = RunStatusFailed
	} else {
		run.Status = RunStatusSucceeded
	}
	_ = r.store.SaveRun(run)
	runMu.Unlock()

	r.mu.Lock()
	delete(r.active, run.ID)
	r.mu.Unlock()
}

func (r *Runner) executeStage(ctx context.Context, run *Run, runMu *sync.Mutex, indexes []int) bool {
	if len(indexes) == 0 {
		return false
	}

	maxActive := run.Options.MaxActiveTasks
	if maxActive < 1 {
		maxActive = 1
	}
	if len(indexes) == 1 {
		maxActive = 1
	}

	jobs := make(chan int)
	var wg sync.WaitGroup
	var failedMu sync.Mutex
	failed := false

	worker := func() {
		defer wg.Done()
		for index := range jobs {
			if ctx.Err() != nil {
				r.markTaskCancelled(run, runMu, index, "run cancelled")
				continue
			}

			taskFailed := r.executeTask(ctx, run, runMu, index)
			if taskFailed {
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
		switch run.Tasks[index].Status {
		case TaskStatusSucceeded, TaskStatusSkipped, TaskStatusCancelled:
			continue
		case TaskStatusFailed:
			failed = true
			continue
		}
		if ctx.Err() != nil {
			r.markTaskCancelled(run, runMu, index, "run cancelled")
			continue
		}
		jobs <- index
	}
	close(jobs)
	wg.Wait()

	return failed
}

func (r *Runner) executeTask(ctx context.Context, run *Run, runMu *sync.Mutex, index int) bool {
	now := time.Now().UTC()
	runMu.Lock()
	recovering := run.Tasks[index].Status == TaskStatusRunning
	if run.Tasks[index].JobName == "" {
		run.Tasks[index].JobName = jobNameForTask(run.ID, run.Tasks[index].ID)
	}
	run.Tasks[index].Status = TaskStatusRunning
	if !recovering {
		run.Tasks[index].PodStatus = PodStatusPending
	}
	run.Tasks[index].StartedAt = &now
	run.Tasks[index].Error = ""
	task := run.Tasks[index]
	_ = r.store.SaveRun(run)
	runMu.Unlock()

	updatePodStatus := func(status PodStatus) {
		runMu.Lock()
		defer runMu.Unlock()
		if run.Tasks[index].PodStatus == status {
			return
		}
		run.Tasks[index].PodStatus = status
		_ = r.store.SaveRun(run)
	}
	exitCode, err := r.executor.RunTask(ctx, run.ID, &task, task.LogPath, recovering, updatePodStatus)

	finished := time.Now().UTC()
	runMu.Lock()
	run.Tasks[index].JobName = task.JobName
	run.Tasks[index].FinishedAt = &finished
	run.Tasks[index].ExitCode = &exitCode
	if ctx.Err() != nil {
		run.Tasks[index].Status = TaskStatusCancelled
		run.Tasks[index].PodStatus = PodStatusTerminating
		run.Tasks[index].Error = "run cancelled"
	} else if err != nil {
		run.Tasks[index].Status = TaskStatusFailed
		run.Tasks[index].PodStatus = PodStatusFailed
		run.Tasks[index].Error = err.Error()
	} else {
		run.Tasks[index].Status = TaskStatusSucceeded
		run.Tasks[index].PodStatus = PodStatusSucceeded
	}
	_ = r.store.SaveRun(run)
	runMu.Unlock()

	return err != nil || exitCode != 0
}

func (r *Runner) markStageSkipped(run *Run, runMu *sync.Mutex, indexes []int) {
	runMu.Lock()
	defer runMu.Unlock()
	now := time.Now().UTC()
	for _, index := range indexes {
		if run.Tasks[index].Status != TaskStatusPending {
			continue
		}
		run.Tasks[index].Status = TaskStatusSkipped
		run.Tasks[index].FinishedAt = &now
		run.Tasks[index].Error = "skipped after prior failure"
	}
	_ = r.store.SaveRun(run)
}

func (r *Runner) markTaskCancelled(run *Run, runMu *sync.Mutex, index int, message string) {
	runMu.Lock()
	defer runMu.Unlock()
	if run.Tasks[index].Status != TaskStatusPending && run.Tasks[index].Status != TaskStatusRunning {
		return
	}
	now := time.Now().UTC()
	run.Tasks[index].Status = TaskStatusCancelled
	run.Tasks[index].FinishedAt = &now
	run.Tasks[index].Error = message
	_ = r.store.SaveRun(run)
}

func buildStages(tasks []Task) [][]int {
	stages := [][]int{}
	sizeIndexes := map[string][]int{
		"small":         {},
		"medium":        {},
		"large":         {},
		enrichmentGroup: {},
	}
	for i, task := range tasks {
		if task.Kind == TaskKindDataset {
			sizeIndexes[task.Size] = append(sizeIndexes[task.Size], i)
			continue
		}
		stages = append(stages, []int{i})
	}

	ordered := [][]int{}
	for _, size := range initialDatasetSizes {
		if len(sizeIndexes[size]) > 0 {
			ordered = append(ordered, sizeIndexes[size])
		}
	}
	ordered = append(ordered, stages...)
	if len(sizeIndexes[enrichmentGroup]) > 0 {
		ordered = append(ordered, sizeIndexes[enrichmentGroup])
	}
	return ordered
}

func newRunID() string {
	return time.Now().UTC().Format("20060102-150405-000000000")
}
