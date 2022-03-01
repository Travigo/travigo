package main

import (
	"os"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/rabbitmq"
	"github.com/britbus/britbus/pkg/realtime"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v2"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	app := &cli.App{
		Name: "realtime",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run the realtime calculation server",
				Flags: []cli.Flag{},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}
					if err := rabbitmq.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to RabbitMQ")
					}

					realtime.StartWorker()

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
