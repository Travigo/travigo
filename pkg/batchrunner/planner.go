package batchrunner

import (
	"fmt"
	"sort"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
)

const postProcessingGroup = "post-processing"

var datasetSizes = []string{"small", "medium", "large"}
var planGroupOrder = []string{"small", "medium", "large", postProcessingGroup}

type fixedTaskDefinition struct {
	id   string
	name string
	kind TaskKind
	args []string
}

var postProcessingTaskDefinitions = []fixedTaskDefinition{
	{id: "link-stops", name: "Link stops", kind: TaskKindLinkStops, args: []string{"data-linker", "run", "--type", "stops"}},
	{id: "link-stop-transfers", name: "Build stop transfers", kind: TaskKindLinkTransfers, args: []string{"data-linker", "run", "--type", "stop-transfers"}},
	{id: "link-services", name: "Link services", kind: TaskKindLinkServices, args: []string{"data-linker", "run", "--type", "services"}},
	{id: "index-stops", name: "Index stops", kind: TaskKindIndexStops, args: []string{"indexer", "stops"}},
}

func BuildPlan() Plan {
	plan := Plan{
		Groups: map[string][]PlanTask{},
	}
	for _, group := range planGroupOrder {
		plan.Groups[group] = []PlanTask{}
	}

	for _, dataset := range manager.GetRegisteredDataSets() {
		if dataset.ImportDestination == datasets.ImportDestinationRealtimeQueue || dataset.ImportDestination == datasets.ImportDestinationSpecificRunner {
			continue
		}

		size := dataset.DatasetSize
		if size == "" {
			size = "small"
		}
		if _, ok := plan.Groups[size]; !ok {
			size = "small"
		}

		plan.Groups[size] = append(plan.Groups[size], PlanTask{
			Identifier: dataset.Identifier,
			Name:       dataset.Identifier,
			Kind:       TaskKindDataset,
			Size:       size,
			Format:     string(dataset.Format),
			Provider:   dataset.Provider.Name,
		})
	}

	for _, size := range datasetSizes {
		sort.Slice(plan.Groups[size], func(i, j int) bool {
			return plan.Groups[size][i].Identifier < plan.Groups[size][j].Identifier
		})
	}
	plan.Groups[postProcessingGroup] = buildPostProcessingPlanTasks()

	return plan
}

func BuildRunTasks(plan Plan, options RunOptions) []Task {
	selected := selectedTaskIDs(options)
	tasks := []Task{}

	for _, group := range planGroupOrder {
		for _, item := range plan.Groups[group] {
			if !includePlanTask(item, selected, options) {
				continue
			}

			task, ok := taskFromPlanTask(item, options)
			if ok {
				tasks = append(tasks, task)
			}
		}
	}

	return tasks
}

func buildPostProcessingPlanTasks() []PlanTask {
	tasks := make([]PlanTask, 0, len(postProcessingTaskDefinitions))
	for _, definition := range postProcessingTaskDefinitions {
		tasks = append(tasks, PlanTask{
			Identifier: definition.id,
			Name:       definition.name,
			Kind:       definition.kind,
		})
	}
	return tasks
}

func selectedTaskIDs(options RunOptions) map[string]bool {
	selected := map[string]bool{}
	for _, id := range options.TaskIDs {
		selected[id] = true
	}
	return selected
}

func includePlanTask(item PlanTask, selected map[string]bool, options RunOptions) bool {
	return options.IncludeAllTasks || selected[item.Identifier]
}

func taskFromPlanTask(item PlanTask, options RunOptions) (Task, bool) {
	if item.Kind == TaskKindDataset {
		args := []string{"data-importer", "dataset", "--id", item.Identifier}
		if options.ForceImport {
			args = append(args, "--force")
		}

		return Task{
			ID:        item.Identifier,
			Name:      item.Identifier,
			Kind:      TaskKindDataset,
			Size:      item.Size,
			DatasetID: item.Identifier,
			Args:      args,
			Status:    TaskStatusPending,
		}, true
	}

	for _, definition := range postProcessingTaskDefinitions {
		if definition.id == item.Identifier {
			task := fixedTask(definition.id, definition.name, definition.kind, definition.args)
			task.Size = postProcessingGroup
			return task, true
		}
	}

	return Task{}, false
}

func fixedTask(id string, name string, kind TaskKind, args []string) Task {
	return Task{
		ID:     id,
		Name:   name,
		Kind:   kind,
		Args:   args,
		Status: TaskStatusPending,
	}
}

func taskStage(task Task) string {
	if task.Kind == TaskKindDataset {
		return task.Size
	}

	return fmt.Sprintf("fixed:%s", task.ID)
}
