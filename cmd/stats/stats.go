package main

import (
	"os"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/elastic_client"
	"github.com/britbus/britbus/pkg/stats"
	"github.com/britbus/britbus/pkg/transforms"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"

	_ "time/tzdata"
)

func main() {
	// Overwrite internal timezone location to UK time
	loc, _ := time.LoadLocation("Europe/London")
	time.Local = loc

	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	transforms.SetupClient()

	app := &cli.App{
		Name: "stats",
		Commands: []*cli.Command{
			{
				Name:  "index",
				Usage: "index the latest data into Elasticsearch",
				Action: func(c *cli.Context) error {
					if err := elastic_client.Connect(true); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Elasticsearch")
					}

					ctdf.LoadSpecialDayCache()

					indexer := stats.Indexer{
						CloudBucketName: "britbus-journey-history",
					}
					indexer.Perform()

					return nil
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
}
