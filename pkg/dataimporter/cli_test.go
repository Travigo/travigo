package dataimporter

import (
	"testing"

	"github.com/urfave/cli/v2"
)

func TestDatasetImporterResourceLimitsAreOptIn(t *testing.T) {
	command := RegisterCLI()

	var datasetCommand *cli.Command
	for _, subcommand := range command.Subcommands {
		if subcommand.Name == "dataset" {
			datasetCommand = subcommand
			break
		}
	}
	if datasetCommand == nil {
		t.Fatal("dataset subcommand not found")
	}

	defaults := map[string]int{}
	for _, flag := range datasetCommand.Flags {
		if intFlag, ok := flag.(*cli.IntFlag); ok {
			defaults[intFlag.Name] = intFlag.Value
		}
	}

	maxCPUs, ok := defaults["max-cpus"]
	if !ok {
		t.Fatal("max-cpus flag not found")
	}
	if maxCPUs != 0 {
		t.Fatalf("max-cpus default = %d, want 0", maxCPUs)
	}

	memoryLimitMB, ok := defaults["memory-limit-mb"]
	if !ok {
		t.Fatal("memory-limit-mb flag not found")
	}
	if memoryLimitMB != 0 {
		t.Fatalf("memory-limit-mb default = %d, want 0", memoryLimitMB)
	}
}
