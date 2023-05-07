package main

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/api"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/urfave/cli/v2"

	_ "time/tzdata"
)

func main() {
	if os.Getenv("TRAVIGO_LOG_FORMAT") != "JSON" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})
	}

	if os.Getenv("TRAVIGO_DEBUG") == "YES" {
		log.Logger = log.Logger.Level(zerolog.DebugLevel)
	} else {
		log.Logger = log.Logger.Level(zerolog.InfoLevel)
	}

	transforms.SetupClient()

	app := &cli.App{
		Name: "web-api",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run web api server",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:  "listen",
						Value: ":8080",
						Usage: "listen target for the web server",
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}
					if err := elastic_client.Connect(false); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Elasticsearch")
					}

					dataaggregator.GlobalSetup()

					ctdf.LoadSpecialDayCache()

					api.SetupServer(c.String("listen"))

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
