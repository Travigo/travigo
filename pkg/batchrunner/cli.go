package batchrunner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "batch-runner",
		Usage: "Run and inspect scheduled Travigo batch imports",
		Subcommands: []*cli.Command{
			{
				Name:  "server",
				Usage: "Start the batch runner UI and API",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "address", Value: ConfigFromEnv().Address, Usage: "HTTP listen address"},
					&cli.StringFlag{Name: "storage-path", Value: ConfigFromEnv().StoragePath, Usage: "Directory used for run metadata and logs"},
				},
				Action: func(c *cli.Context) error {
					config := ConfigFromEnv()
					config.Address = c.String("address")
					config.StoragePath = c.String("storage-path")

					store, err := NewStore(config.StoragePath)
					if err != nil {
						return err
					}
					if err := store.MarkInterruptedRuns(); err != nil {
						return err
					}

					executor, err := NewKubernetesExecutor(config)
					if err != nil {
						return err
					}

					runner := NewRunner(config, store, executor)
					server := NewServer(store, runner)

					log.Info().Str("address", config.Address).Str("storage", config.StoragePath).Msg("Starting batch runner")
					return http.ListenAndServe(config.Address, server.Handler())
				},
			},
			{
				Name:  "plan",
				Usage: "Print the static dataset batch plan",
				Action: func(c *cli.Context) error {
					encoder := json.NewEncoder(c.App.Writer)
					encoder.SetIndent("", "  ")
					return encoder.Encode(BuildPlan())
				},
			},
			{
				Name:  "trigger",
				Usage: "Start a run through a batch runner API",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "server", Value: "http://travigo-data-importer-batch-runner:8080", Usage: "Batch runner API base URL"},
					&cli.StringSliceFlag{Name: "dataset", Usage: "Dataset ID to run; repeat for multiple datasets"},
					&cli.BoolFlag{Name: "force", Usage: "Force dataset imports"},
					&cli.IntFlag{Name: "max-active-tasks", Value: 1, Usage: "Maximum child jobs to run concurrently"},
					&cli.BoolFlag{Name: "continue-on-failure", Value: true, Usage: "Continue later stages after failures"},
					&cli.BoolFlag{Name: "skip-link-stops", Usage: "Skip stop linking"},
					&cli.BoolFlag{Name: "skip-link-transfers", Usage: "Skip stop transfer building"},
					&cli.BoolFlag{Name: "skip-link-services", Usage: "Skip service linking"},
					&cli.BoolFlag{Name: "skip-index-stops", Usage: "Skip stop indexing"},
				},
				Action: func(c *cli.Context) error {
					options := RunOptions{
						DatasetIDs:          c.StringSlice("dataset"),
						IncludeAllDatasets:  len(c.StringSlice("dataset")) == 0,
						IncludeLinkStops:    !c.Bool("skip-link-stops"),
						IncludeTransfers:    !c.Bool("skip-link-transfers"),
						IncludeLinkServices: !c.Bool("skip-link-services"),
						IncludeIndexStops:   !c.Bool("skip-index-stops"),
						ForceImport:         c.Bool("force"),
						MaxActiveTasks:      c.Int("max-active-tasks"),
						ContinueOnFailure:   c.Bool("continue-on-failure"),
					}
					return triggerRun(c.String("server"), options)
				},
			},
		},
	}
}

func triggerRun(baseURL string, options RunOptions) error {
	if baseURL == "" {
		return errors.New("server is required")
	}

	data, err := json.Marshal(options)
	if err != nil {
		return err
	}

	resp, err := http.Post(strings.TrimRight(baseURL, "/")+"/api/runs", "application/json", bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if body["error"] != "" {
			return errors.New(body["error"])
		}
		return fmt.Errorf("batch runner returned %s", resp.Status)
	}

	var run Run
	if err := json.NewDecoder(resp.Body).Decode(&run); err != nil {
		return err
	}

	log.Info().Str("run", run.ID).Msg("Batch run triggered")
	return nil
}
