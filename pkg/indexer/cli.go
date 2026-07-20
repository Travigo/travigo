package indexer

import (
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	dataaggregator "github.com/travigo/travigo/pkg/dataaggregator/global"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/redis_client"
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
					if err := redis_client.Connect(); err != nil {
						return err
					}

					dataaggregator.Setup()

					if err := IndexStops(); err != nil {
						return err
					}

					elastic_client.WaitUntilQueueEmpty()

					log.Info().Msg("Index queue emptied")

					deleteLegacyStopIndexes()

					if err := datasetversion.Upsert(c.Context, ctdf.DatasetVersion{
						Dataset:      datasetversion.StopsIndexerDataset,
						LastModified: time.Now(),
					}); err != nil {
						return err
					}

					return nil
				},
			},
		},
	}
}
