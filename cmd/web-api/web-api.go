package main

import (
	"os"
	"time"

	"github.com/britbus/britbus/pkg/api"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/elastic_client"
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
					if err := elastic_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Elasticsearch")
					}

					// servicesCollection := database.GetCollection("stops")
					// var service *ctdf.Stop
					// servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": "GB:ATCO:0500CCITY346"}).Decode(&service)
					// service.GetServices()

					// transforms.Transform([]*ctdf.Stop{service})

					// pretty.Println(service)

					// return nil

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
