package batchrunner

import "testing"

func TestBuildRunTasksDatasetSelection(t *testing.T) {
	plan := Plan{
		Groups: map[string][]DatasetPlan{
			"small": {
				{Identifier: "small-a", Size: "small"},
			},
			"medium": {
				{Identifier: "medium-a", Size: "medium"},
			},
			"large": {},
		},
	}

	allTasks := BuildRunTasks(plan, RunOptions{IncludeAllDatasets: true})
	if len(allTasks) != 2 {
		t.Fatalf("expected all datasets to produce 2 tasks, got %d", len(allTasks))
	}

	noDatasetTasks := BuildRunTasks(plan, RunOptions{IncludeLinkStops: true})
	if len(noDatasetTasks) != 1 {
		t.Fatalf("expected only fixed task, got %d tasks", len(noDatasetTasks))
	}
	if noDatasetTasks[0].Kind != TaskKindLinkStops {
		t.Fatalf("expected link stops task, got %s", noDatasetTasks[0].Kind)
	}

	selectedTasks := BuildRunTasks(plan, RunOptions{DatasetIDs: []string{"medium-a"}})
	if len(selectedTasks) != 1 {
		t.Fatalf("expected one selected dataset task, got %d", len(selectedTasks))
	}
	if selectedTasks[0].DatasetID != "medium-a" {
		t.Fatalf("expected medium-a, got %s", selectedTasks[0].DatasetID)
	}
}

func TestBuildStages(t *testing.T) {
	tasks := []Task{
		{ID: "small-a", Kind: TaskKindDataset, Size: "small"},
		{ID: "large-a", Kind: TaskKindDataset, Size: "large"},
		{ID: "medium-a", Kind: TaskKindDataset, Size: "medium"},
		{ID: "link-stops", Kind: TaskKindLinkStops},
	}

	stages := buildStages(tasks)
	if len(stages) != 4 {
		t.Fatalf("expected 4 stages, got %d", len(stages))
	}

	expected := [][]int{{0}, {2}, {1}, {3}}
	for i := range expected {
		if len(stages[i]) != len(expected[i]) {
			t.Fatalf("stage %d length mismatch", i)
		}
		for j := range expected[i] {
			if stages[i][j] != expected[i][j] {
				t.Fatalf("stage %d index %d: expected %d, got %d", i, j, expected[i][j], stages[i][j])
			}
		}
	}
}

func TestJobNameForTask(t *testing.T) {
	first := jobNameForTask("20260706-050000-000000001", "gb-dft-bods-gtfs-schedule-east-anglia")
	second := jobNameForTask("20260706-050000-000000001", "gb-dft-bods-gtfs-schedule-east-midlands")

	if len(first) > 63 {
		t.Fatalf("job name is too long: %s", first)
	}
	if first == second {
		t.Fatalf("expected distinct job names after truncation")
	}
}
