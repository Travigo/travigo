package indexer

import (
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "indexer",
		Usage: "Indexes data into Elasticsearch",
		Subcommands: []*cli.Command{
			{
				Name:  "stops",
				Usage: "do an index of the Stops",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(true); err != nil {
						return err
					}

					IndexStops()

					elastic_client.WaitUntilQueueEmpty()

					log.Info().Msg("Index queue emptied")

					return nil
				},
			},
		},
	}
}
