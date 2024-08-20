package dataimporter

import (
	"time"

	"github.com/travigo/travigo/pkg/dataimporter/insertrecords"
	"github.com/travigo/travigo/pkg/dataimporter/manager"

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
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Redis")
					}
					insertrecords.Insert()

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
		},
	}
}
