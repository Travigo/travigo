package batchrunner

import "time"

type TaskKind string

const (
	TaskKindDataset       TaskKind = "dataset"
	TaskKindLinkStops     TaskKind = "link-stops"
	TaskKindLinkTransfers TaskKind = "link-stop-transfers"
	TaskKindLinkServices  TaskKind = "link-services"
	TaskKindLinkJourneys  TaskKind = "link-journeys"
	TaskKindIndexStops    TaskKind = "index-stops"
)

type TaskStatus string

const (
	TaskStatusPending   TaskStatus = "pending"
	TaskStatusRunning   TaskStatus = "running"
	TaskStatusSucceeded TaskStatus = "succeeded"
	TaskStatusFailed    TaskStatus = "failed"
	TaskStatusSkipped   TaskStatus = "skipped"
	TaskStatusCancelled TaskStatus = "cancelled"
)

type PodStatus string

const (
	PodStatusPending     PodStatus = "pending"
	PodStatusRunning     PodStatus = "running"
	PodStatusTerminating PodStatus = "terminating"
	PodStatusSucceeded   PodStatus = "succeeded"
	PodStatusFailed      PodStatus = "failed"
)

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

type PlanTask struct {
	Identifier string   `json:"identifier" groups:"web-plan"`
	Name       string   `json:"name,omitempty" groups:"web-plan"`
	Kind       TaskKind `json:"kind" groups:"web-plan"`
	Size       string   `json:"size,omitempty" groups:"web-plan"`
	Format     string   `json:"format,omitempty" groups:"web-plan"`
	Provider   string   `json:"provider,omitempty" groups:"web-plan"`
}

type Plan struct {
	Groups map[string][]PlanTask `json:"groups" groups:"web-plan"`
}

type RunOptions struct {
	TaskIDs           []string `json:"taskIds"`
	IncludeAllTasks   bool     `json:"includeAllTasks"`
	ForceImport       bool     `json:"forceImport"`
	MaxActiveTasks    int      `json:"maxActiveTasks"`
	ContinueOnFailure bool     `json:"continueOnFailure"`
}

type Run struct {
	ID              string     `json:"id" groups:"web-run-summary,web-run-detail"`
	Status          RunStatus  `json:"status" groups:"web-run-summary,web-run-detail"`
	Options         RunOptions `json:"options"`
	Tasks           []Task     `json:"tasks" groups:"web-run-detail"`
	CreatedAt       time.Time  `json:"createdAt" groups:"web-run-summary,web-run-detail"`
	StartedAt       *time.Time `json:"startedAt,omitempty" groups:"web-run-detail"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty" groups:"web-run-detail"`
	Error           string     `json:"error,omitempty" groups:"web-run-detail"`
	CancelRequested bool       `json:"cancelRequested"`
}

type Task struct {
	ID         string        `json:"id" groups:"web-run-detail"`
	Name       string        `json:"name" groups:"web-run-detail"`
	Kind       TaskKind      `json:"kind" groups:"web-run-detail"`
	Size       string        `json:"size,omitempty" groups:"web-run-detail"`
	DatasetID  string        `json:"datasetId,omitempty"`
	Args       []string      `json:"args"`
	Status     TaskStatus    `json:"status" groups:"web-run-detail"`
	PodStatus  PodStatus     `json:"podStatus,omitempty" groups:"web-run-detail"`
	JobName    string        `json:"jobName,omitempty" groups:"web-run-detail"`
	LogPath    string        `json:"logPath,omitempty"`
	StartedAt  *time.Time    `json:"startedAt,omitempty"`
	FinishedAt *time.Time    `json:"finishedAt,omitempty"`
	ExitCode   *int          `json:"exitCode,omitempty"`
	Error      string        `json:"error,omitempty" groups:"web-run-detail"`
	Attempts   []TaskAttempt `json:"attempts,omitempty" groups:"web-run-detail"`
}

// TaskAttempt records one Pod created by a Kubernetes Job. A Job can create
// multiple Pods when Kubernetes retries it, so this is deliberately separate
// from the task's current, top-level PodStatus.
type TaskAttempt struct {
	Attempt    int        `json:"attempt" groups:"web-run-detail"`
	PodName    string     `json:"podName" groups:"web-run-detail"`
	NodeName   string     `json:"nodeName,omitempty" groups:"web-run-detail"`
	Status     PodStatus  `json:"status" groups:"web-run-detail"`
	CreatedAt  *time.Time `json:"createdAt,omitempty" groups:"web-run-detail"`
	StartedAt  *time.Time `json:"startedAt,omitempty" groups:"web-run-detail"`
	FinishedAt *time.Time `json:"finishedAt,omitempty" groups:"web-run-detail"`
	ExitCode   *int       `json:"exitCode,omitempty" groups:"web-run-detail"`
	Reason     string     `json:"reason,omitempty" groups:"web-run-detail"`
	Message    string     `json:"message,omitempty" groups:"web-run-detail"`
}

func defaultRunOptions(options RunOptions) RunOptions {
	if options.MaxActiveTasks < 1 {
		options.MaxActiveTasks = 1
	}

	return options
}
