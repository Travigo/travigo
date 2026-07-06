package batchrunner

import "time"

type TaskKind string

const (
	TaskKindDataset       TaskKind = "dataset"
	TaskKindLinkStops     TaskKind = "link-stops"
	TaskKindLinkTransfers TaskKind = "link-stop-transfers"
	TaskKindLinkServices  TaskKind = "link-services"
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

type RunStatus string

const (
	RunStatusPending   RunStatus = "pending"
	RunStatusRunning   RunStatus = "running"
	RunStatusSucceeded RunStatus = "succeeded"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCancelled RunStatus = "cancelled"
)

type PlanTask struct {
	Identifier string   `json:"identifier"`
	Name       string   `json:"name,omitempty"`
	Kind       TaskKind `json:"kind"`
	Size       string   `json:"size,omitempty"`
	Format     string   `json:"format,omitempty"`
	Provider   string   `json:"provider,omitempty"`
}

type Plan struct {
	Groups map[string][]PlanTask `json:"groups"`
}

type RunOptions struct {
	TaskIDs           []string `json:"taskIds"`
	IncludeAllTasks   bool     `json:"includeAllTasks"`
	ForceImport       bool     `json:"forceImport"`
	MaxActiveTasks    int      `json:"maxActiveTasks"`
	ContinueOnFailure bool     `json:"continueOnFailure"`
}

type Run struct {
	ID              string     `json:"id"`
	Status          RunStatus  `json:"status"`
	Options         RunOptions `json:"options"`
	Tasks           []Task     `json:"tasks"`
	CreatedAt       time.Time  `json:"createdAt"`
	StartedAt       *time.Time `json:"startedAt,omitempty"`
	FinishedAt      *time.Time `json:"finishedAt,omitempty"`
	Error           string     `json:"error,omitempty"`
	CancelRequested bool       `json:"cancelRequested"`
}

type Task struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Kind       TaskKind   `json:"kind"`
	Size       string     `json:"size,omitempty"`
	DatasetID  string     `json:"datasetId,omitempty"`
	Args       []string   `json:"args"`
	Status     TaskStatus `json:"status"`
	JobName    string     `json:"jobName,omitempty"`
	LogPath    string     `json:"logPath,omitempty"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`
	ExitCode   *int       `json:"exitCode,omitempty"`
	Error      string     `json:"error,omitempty"`
}

func defaultRunOptions(options RunOptions) RunOptions {
	if options.MaxActiveTasks < 1 {
		options.MaxActiveTasks = 1
	}

	return options
}
