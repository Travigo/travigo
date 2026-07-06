package batchrunner

import (
	"fmt"
	"sort"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
)

var datasetSizes = []string{"small", "medium", "large"}

func BuildPlan() Plan {
	plan := Plan{
		Groups: map[string][]DatasetPlan{
			"small":  {},
			"medium": {},
			"large":  {},
		},
	}

	for _, dataset := range manager.GetRegisteredDataSets() {
		if dataset.ImportDestination == datasets.ImportDestinationRealtimeQueue {
			continue
		}

		size := dataset.DatasetSize
		if size == "" {
			size = "small"
		}
		if _, ok := plan.Groups[size]; !ok {
			size = "small"
		}

		plan.Groups[size] = append(plan.Groups[size], DatasetPlan{
			Identifier: dataset.Identifier,
			Size:       size,
			Format:     string(dataset.Format),
			Provider:   dataset.Provider.Name,
		})
	}

	for size := range plan.Groups {
		sort.Slice(plan.Groups[size], func(i, j int) bool {
			return plan.Groups[size][i].Identifier < plan.Groups[size][j].Identifier
		})
	}

	return plan
}

func BuildRunTasks(plan Plan, options RunOptions) []Task {
	selected := map[string]bool{}
	for _, id := range options.DatasetIDs {
		selected[id] = true
	}
	includeAllDatasets := options.IncludeAllDatasets

	tasks := []Task{}
	for _, size := range datasetSizes {
		for _, dataset := range plan.Groups[size] {
			if !includeAllDatasets && !selected[dataset.Identifier] {
				continue
			}

			args := []string{"data-importer", "dataset", "--id", dataset.Identifier}
			if options.ForceImport {
				args = append(args, "--force")
			}

			tasks = append(tasks, Task{
				ID:        dataset.Identifier,
				Name:      dataset.Identifier,
				Kind:      TaskKindDataset,
				Size:      size,
				DatasetID: dataset.Identifier,
				Args:      args,
				Status:    TaskStatusPending,
			})
		}
	}

	if options.IncludeLinkStops {
		tasks = append(tasks, fixedTask("link-stops", "Link stops", TaskKindLinkStops, []string{"data-linker", "run", "--type", "stops"}))
	}
	if options.IncludeTransfers {
		tasks = append(tasks, fixedTask("link-stop-transfers", "Build stop transfers", TaskKindLinkTransfers, []string{"data-linker", "run", "--type", "stop-transfers"}))
	}
	if options.IncludeLinkServices {
		tasks = append(tasks, fixedTask("link-services", "Link services", TaskKindLinkServices, []string{"data-linker", "run", "--type", "services"}))
	}
	if options.IncludeIndexStops {
		tasks = append(tasks, fixedTask("index-stops", "Index stops", TaskKindIndexStops, []string{"indexer", "stops"}))
	}

	return tasks
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
