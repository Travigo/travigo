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
			"large":                {},
			postProcessingGroup:    buildPostProcessingPlanTasks(postProcessingGroup),
			journeyPublishingGroup: buildPostProcessingPlanTasks(journeyPublishingGroup),
			journeyEnrichmentGroup: {},
			enrichmentGroup:        {},
		},
	}

	noTasks := BuildRunTasks(plan, RunOptions{})
	if len(noTasks) != 0 {
		t.Fatalf("expected no tasks without selected ids, got %d", len(noTasks))
	}

	allTasks := BuildRunTasks(plan, RunOptions{IncludeAllTasks: true})
	if len(allTasks) != 9 {
		t.Fatalf("expected all plan tasks to produce 9 tasks, got %d", len(allTasks))
	}
	if allTasks[2].Kind != TaskKindRemoveRetiredDatasets || allTasks[3].Kind != TaskKindLinkStops {
		t.Fatalf("expected retired dataset cleanup after imports and before relinking, got %+v", allTasks)
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
		{ID: "osm-tracks", Kind: TaskKindDataset, Size: journeyEnrichmentGroup},
		{ID: "link-journeys", Kind: TaskKindLinkJourneys},
		{ID: "enrich-a", Kind: TaskKindDataset, Size: enrichmentGroup},
	}

	stages := buildStages(tasks)
	if len(stages) != 7 {
		t.Fatalf("expected 7 stages, got %d", len(stages))
	}

	expected := [][]int{{0}, {2}, {1}, {3}, {4}, {5}, {6}}
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
		journeyPublishingGroup: buildPostProcessingPlanTasks(journeyPublishingGroup),
	}}, RunOptions{TaskIDs: []string{"link-journeys"}})

	if len(tasks) != 1 {
		t.Fatalf("expected one journey publisher task, got %d", len(tasks))
	}
	if tasks[0].Kind != TaskKindLinkJourneys {
		t.Fatalf("expected journey linker kind, got %s", tasks[0].Kind)
	}
	expectedArgs := []string{"data-linker", "run", "--type", "journeys", "--skip-staging"}
	if !slices.Equal(tasks[0].Args, expectedArgs) {
		t.Fatalf("journey publisher args = %q, expected %q", tasks[0].Args, expectedArgs)
	}
}

func TestStopServicesRefreshTaskSelectionAndOrder(t *testing.T) {
	plan := Plan{Groups: map[string][]PlanTask{
		journeyPublishingGroup: buildPostProcessingPlanTasks(journeyPublishingGroup),
	}}

	selected := BuildRunTasks(plan, RunOptions{TaskIDs: []string{"refresh-stop-services"}})
	if len(selected) != 1 {
		t.Fatalf("expected one selected stop-services task, got %d", len(selected))
	}
	if selected[0].Kind != TaskKindStopServices {
		t.Fatalf("selected task kind = %s, want %s", selected[0].Kind, TaskKindStopServices)
	}
	wantArgs := []string{"data-linker", "stop-services", "pending", "--timeout", "90m"}
	if !slices.Equal(selected[0].Args, wantArgs) {
		t.Fatalf("stop-services task args = %q, want %q", selected[0].Args, wantArgs)
	}

	all := BuildRunTasks(plan, RunOptions{IncludeAllTasks: true})
	journeyIndex := slices.IndexFunc(all, func(task Task) bool { return task.ID == "link-journeys" })
	refreshIndex := slices.IndexFunc(all, func(task Task) bool { return task.ID == "refresh-stop-services" })
	indexIndex := slices.IndexFunc(all, func(task Task) bool { return task.ID == "index-stops" })
	if journeyIndex < 0 || refreshIndex != journeyIndex+1 || indexIndex != refreshIndex+1 {
		t.Fatalf("journey refresh order = link:%d refresh:%d index:%d", journeyIndex, refreshIndex, indexIndex)
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

func TestOSMRailTracksRunsBeforeJourneyPublishing(t *testing.T) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("../.."); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(workingDirectory)

	plan := BuildPlan()
	enrichment := plan.Groups[journeyEnrichmentGroup]
	if len(enrichment) != 2 || enrichment[0].Identifier != "global-openstreetmap-gb-national-rail-tracks" || enrichment[1].Kind != TaskKindTDMappings {
		t.Fatalf("journey enrichment group = %+v", enrichment)
	}

	tasks := BuildRunTasks(plan, RunOptions{IncludeAllTasks: true})
	serviceLinkIndex := slices.IndexFunc(tasks, func(task Task) bool { return task.ID == "link-services" })
	osmIndex := slices.IndexFunc(tasks, func(task Task) bool { return task.ID == "global-openstreetmap-gb-national-rail-tracks" })
	journeyLinkIndex := slices.IndexFunc(tasks, func(task Task) bool { return task.ID == "link-journeys" })
	refreshIndex := slices.IndexFunc(tasks, func(task Task) bool { return task.ID == "refresh-stop-services" })
	indexStopsIndex := slices.IndexFunc(tasks, func(task Task) bool { return task.ID == "index-stops" })
	if serviceLinkIndex < 0 || osmIndex < 0 || journeyLinkIndex < 0 || refreshIndex != journeyLinkIndex+1 || indexStopsIndex != refreshIndex+1 || !(serviceLinkIndex < osmIndex && osmIndex < journeyLinkIndex) {
		t.Fatalf("pipeline positions: services=%d osm=%d journeys=%d", serviceLinkIndex, osmIndex, journeyLinkIndex)
	}
	berthIndex := slices.IndexFunc(tasks, func(task Task) bool { return task.Kind == TaskKindTDMappings })
	if berthIndex != osmIndex+1 || tasks[berthIndex+1].Kind != TaskKindLinkJourneys {
		t.Fatalf("expected berth import between OSM tracks and journey publication, got index %d", berthIndex)
	}
	stages := buildStages(tasks)
	stage := slices.IndexFunc(stages, func(indices []int) bool { return slices.Contains(indices, berthIndex) })
	if stage < 1 || !slices.Equal(stages[stage], []int{berthIndex}) || !slices.Contains(stages[stage-1], osmIndex) {
		t.Fatalf("berth import must wait for the OSM stage, got %v", stages)
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
