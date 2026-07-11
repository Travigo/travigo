package batchrunner

import (
	"context"
	"testing"
	"time"
)

type recordingExecutor struct {
	tasks      chan Task
	recovering chan bool
}

func (e *recordingExecutor) RunTask(_ context.Context, _ string, task *Task, _ string, recovering bool) (int, error) {
	e.tasks <- *task
	e.recovering <- recovering
	return 0, nil
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
			ID:      "link-stops",
			Kind:    TaskKindLinkStops,
			Status:  TaskStatusRunning,
			JobName: "travigo-batch-20260711-120000-000000000-link-stops",
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
			return
		}
		time.Sleep(10 * time.Millisecond)
	}

	t.Fatal("recovered run did not complete")
}
