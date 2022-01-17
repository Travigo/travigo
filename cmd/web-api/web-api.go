package main

import (
	"os"
	"time"

	"github.com/britbus/britbus/pkg/api"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

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
