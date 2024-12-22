package dataimporter

import (
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"

	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/urfave/cli/v2"

	"github.com/rs/zerolog/log"

	_ "time/tzdata"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "data-importer",
		Usage: "Download & convert third party datasets into CTDF",
		Subcommands: []*cli.Command{
			{
				Name:  "dataset",
				Usage: "Import a dataset",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "id",
						Usage:    "ID of the dataset",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "repeat-every",
						Usage:    "Repeat this file import every X seconds",
						Required: false,
					},
					&cli.BoolFlag{
						Name:  "force",
						Usage: "Force the import of the dataset",
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Redis")
					}

					datasetid := c.String("id")
					forceImport := c.Bool("force")

					repeatEvery := c.String("repeat-every")
					repeat := repeatEvery != ""
					var repeatDuration time.Duration
					if repeat {
						var err error
						repeatDuration, err = time.ParseDuration(repeatEvery)

						if err != nil {
							return err
						}
					}

					dataset, err := manager.GetDataset(datasetid)
					if err != nil {
						return err
					}

					for {
						startTime := time.Now()

						err := manager.ImportDataset(&dataset, forceImport)

						if err != nil {
							return err
						}
						if !repeat {
							break
						}

						executionDuration := time.Since(startTime)
						log.Info().Msgf("Operation took %s", executionDuration.String())

						waitTime := repeatDuration - executionDuration

						if waitTime.Seconds() > 0 {
							time.Sleep(waitTime)
						}
					}

					return nil
				},
			},
			{
				Name:  "multi-realtime",
				Usage: "Import mutliple realtime datasets",
				Flags: []cli.Flag{},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Redis")
					}

					allDatasets := manager.GetRegisteredDataSets()

					for _, dataset := range allDatasets {
						if dataset.ImportDestination != datasets.ImportDestinationRealtimeQueue {
							continue
						}

						go func(dataset datasets.DataSet) {
							var repeatDuration time.Duration

							if dataset.RefreshInterval.Seconds() > 0 {
								repeatDuration = dataset.RefreshInterval
							} else if dataset.SupportedObjects.RealtimeJourneys {
								repeatDuration = 2 * time.Minute
							} else if dataset.SupportedObjects.ServiceAlerts {
								repeatDuration = 10 * time.Minute
							}

							log.Info().Str("interval", repeatDuration.String()).Str("id", dataset.Identifier).Msg("Loaded realtime dataset")

							for {
								startTime := time.Now()

								err := manager.ImportDataset(&dataset, false)

								if err != nil {
									// TODO report failure here
									log.Error().Err(err).Str("id", dataset.Identifier).Msg("Failed to import dataset")
									time.Sleep(1 * time.Minute)
								}

								executionDuration := time.Since(startTime)
								log.Info().Str("id", dataset.Identifier).Msgf("Operation took %s", executionDuration.String())

								waitTime := repeatDuration - executionDuration

								if waitTime.Seconds() > 0 {
									time.Sleep(waitTime)
								}
							}
						}(dataset)
					}

					signals := make(chan os.Signal, 1)
					signal.Notify(signals, syscall.SIGINT)
					defer signal.Stop(signals)

					<-signals // wait for signal
					go func() {
						<-signals // hard exit on second signal (in case shutdown gets stuck)
						os.Exit(1)
					}()

					return nil
				},
			},
		},
	}
}
