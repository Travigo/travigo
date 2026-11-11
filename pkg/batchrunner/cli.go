package batchrunner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

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
				Usage: "Start the batch runner API",
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
					cleanupStore := func() {
						if err := store.Cleanup(time.Now()); err != nil {
							log.Error().Err(err).Msg("Batch runner storage cleanup failed")
						}
					}
					cleanupStore()
					cleanupContext, cancelCleanup := context.WithCancel(c.Context)
					defer cancelCleanup()
					go runStoreCleanup(cleanupContext, cleanupStore)
					executor, err := NewKubernetesExecutor(config)
					if err != nil {
						return err
					}

					runner := NewRunner(config, store, executor)
					if config.ArtifactBaseURL != "" {
						artifactListener, err := net.Listen("tcp", config.ArtifactAddress)
						if err != nil {
							return fmt.Errorf("start required batch artifact listener: %w", err)
						}
						defer artifactListener.Close()
						go func() {
							if err := http.Serve(artifactListener, store.ArtifactHandler()); err != nil && !errors.Is(err, net.ErrClosed) {
								log.Error().Err(err).Msg("Batch artifact listener stopped")
							}
						}()
					}
					if err := runner.ResumeRuns(); err != nil {
						return err
					}
					server := NewServer(store, runner)

					log.Info().Str("address", config.Address).Str("artifact_address", config.ArtifactAddress).Str("storage", config.StoragePath).Msg("Starting batch runner")
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
					&cli.StringSliceFlag{Name: "task", Usage: "Plan task ID to run; repeat for multiple tasks. Defaults to all tasks."},
					&cli.BoolFlag{Name: "force", Usage: "Force dataset imports"},
					&cli.IntFlag{Name: "max-active-tasks", Value: 1, Usage: "Maximum child jobs to run concurrently"},
					&cli.BoolFlag{Name: "continue-on-failure", Value: true, Usage: "Continue later stages after failures"},
					&cli.BoolFlag{Name: "smart-mode", Usage: "Skip unchanged datasets using stored artifacts"},
				},
				Action: func(c *cli.Context) error {
					taskIDs := c.StringSlice("task")
					options := RunOptions{
						TaskIDs:           taskIDs,
						IncludeAllTasks:   len(taskIDs) == 0,
						ForceImport:       c.Bool("force"),
						MaxActiveTasks:    c.Int("max-active-tasks"),
						ContinueOnFailure: c.Bool("continue-on-failure"),
						SmartMode:         c.Bool("smart-mode"),
					}
					return triggerRun(c.String("server"), options)
				},
			},
		},
	}
}

func runStoreCleanup(ctx context.Context, cleanup func()) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			cleanup()
		}
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

	request, err := http.NewRequest(http.MethodPost, strings.TrimRight(baseURL, "/")+"/runs", bytes.NewReader(data))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	if token := os.Getenv("TRAVIGO_BATCH_RUNNER_AUTH_TOKEN"); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := http.DefaultClient.Do(request)
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
