package cli

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/stats"
	"github.com/travigo/travigo/pkg/stats/web_api"
	"github.com/urfave/cli/v2"
)

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "stats",
		Usage: "Provides indexing & statistics API endpoints",
		Subcommands: []*cli.Command{
			{
				Name:  "index",
				Usage: "index the latest data into Elasticsearch",
				Action: func(c *cli.Context) error {
					if err := elastic_client.Connect(true); err != nil {
						return err
					}

					ctdf.LoadSpecialDayCache()

					indexer := stats.Indexer{
						CloudBucketName: "britbus-journey-history",
					}
					indexer.Perform()

					elastic_client.WaitUntilQueueEmpty()

					return nil
				},
			},
			{
				Name:  "run",
				Usage: "run stats server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "listen",
						Value: ":8081",
						Usage: "listen target for the web server",
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(true); err != nil {
						return err
					}

					web_api.SetupServer(c.String("listen"))

					return nil
				},
			},
		},
	}
}
