package batchrunner

import (
	"os"
	"slices"
	"testing"
)

func TestBuildRunTasksDatasetSelection(t *testing.T) {
	plan := Plan{
		Groups: map[string][]PlanTask{
			"small": {
				{Identifier: "small-a", Kind: TaskKindDataset, Size: "small"},
			},
			"medium": {
				{Identifier: "medium-a", Kind: TaskKindDataset, Size: "medium"},
			},
			"large":             {},
			postProcessingGroup: buildPostProcessingPlanTasks(),
		},
	}

	noTasks := BuildRunTasks(plan, RunOptions{})
	if len(noTasks) != 0 {
		t.Fatalf("expected no tasks without selected ids, got %d", len(noTasks))
	}

	allTasks := BuildRunTasks(plan, RunOptions{IncludeAllTasks: true})
	if len(allTasks) != 7 {
		t.Fatalf("expected all plan tasks to produce 7 tasks, got %d", len(allTasks))
	}

	selectedTasks := BuildRunTasks(plan, RunOptions{TaskIDs: []string{"medium-a", "link-stops"}})
	if len(selectedTasks) != 2 {
		t.Fatalf("expected two selected tasks, got %d", len(selectedTasks))
	}
	if selectedTasks[0].DatasetID != "medium-a" {
		t.Fatalf("expected medium-a, got %s", selectedTasks[0].DatasetID)
	}
	if selectedTasks[1].Kind != TaskKindLinkStops {
		t.Fatalf("expected link stops after dataset task, got %s", selectedTasks[1].Kind)
	}
}

func TestBuildStages(t *testing.T) {
	tasks := []Task{
		{ID: "small-a", Kind: TaskKindDataset, Size: "small"},
		{ID: "large-a", Kind: TaskKindDataset, Size: "large"},
		{ID: "medium-a", Kind: TaskKindDataset, Size: "medium"},
		{ID: "link-stops", Kind: TaskKindLinkStops},
		{ID: "link-journeys", Kind: TaskKindLinkJourneys},
		{ID: "enrich-a", Kind: TaskKindDataset, Size: enrichmentGroup},
	}

	stages := buildStages(tasks)
	if len(stages) != 6 {
		t.Fatalf("expected 6 stages, got %d", len(stages))
	}

	expected := [][]int{{0}, {2}, {1}, {3}, {4}, {5}}
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

func TestJourneyPublisherTask(t *testing.T) {
	tasks := BuildRunTasks(Plan{Groups: map[string][]PlanTask{
		postProcessingGroup: buildPostProcessingPlanTasks(),
	}}, RunOptions{TaskIDs: []string{"link-journeys"}})

	if len(tasks) != 1 {
		t.Fatalf("expected one journey publisher task, got %d", len(tasks))
	}
	if tasks[0].Kind != TaskKindLinkJourneys {
		t.Fatalf("expected journey linker kind, got %s", tasks[0].Kind)
	}
	expectedArgs := []string{"data-linker", "run", "--type", "journeys"}
	if !slices.Equal(tasks[0].Args, expectedArgs) {
		t.Fatalf("journey publisher args = %q, expected %q", tasks[0].Args, expectedArgs)
	}
}

func TestBuildPlanIncludesTfLRouteTracks(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	plan := BuildPlan()
	found := false
	for _, task := range plan.Groups[enrichmentGroup] {
		if task.Identifier == "gb-tfl-route-tracks" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("TfL route tracks dataset was not included in the normal enrichment batch stage")
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
