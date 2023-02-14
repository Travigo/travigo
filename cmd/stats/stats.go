package main

import (
	"os"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/elastic_client"
	"github.com/britbus/britbus/pkg/stats"
	"github.com/britbus/britbus/pkg/stats/web_api"
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

	if os.Getenv("BRITBUS_LOG_FORMAT") != "JSON" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	if os.Getenv("BRITBUS_DEBUG") == "YES" {
		log.Logger = log.Logger.Level(zerolog.DebugLevel)
	} else {
		log.Logger = log.Logger.Level(zerolog.InfoLevel)
	}

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
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}
					if err := elastic_client.Connect(true); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Elasticsearch")
					}

					web_api.SetupServer(c.String("listen"))

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
