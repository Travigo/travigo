package main

import (
	"os"
	"time"

	"github.com/britbus/britbus/pkg/archiver"
	"github.com/britbus/britbus/pkg/database"
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

	app := &cli.App{
		Name: "archiver",
		Commands: []*cli.Command{
			{
				Name:  "run",
				Usage: "run archiver - takes realtime journeys out of database and puts in object store",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "output-directory",
						Usage:    "Directory to write output files to",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to database")
					}

					archiver := archiver.Archiver{
						OutputDirectory:     c.String("output-directory"),
						WriteIndividualFile: false,
						WriteBundle:         true,
						CloudUpload:         true,
						CloudBucketName:     "britbus-journey-history",
					}
					archiver.Perform()

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
