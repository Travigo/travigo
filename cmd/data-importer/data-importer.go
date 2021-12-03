package main

import (
	"errors"
	"os"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/britbus/britbus/pkg/transxchange"
	"github.com/urfave/cli/v2"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	app := &cli.App{
		Name: "data-importer",
		Commands: []*cli.Command{
			{
				Name:  "naptan",
				Usage: "NaPTAN stop location data",
				Subcommands: []*cli.Command{
					{
						Name:      "import-file",
						Usage:     "import an XML file",
						ArgsUsage: "<file path>",
						Action: func(c *cli.Context) error {
							if c.Args().Len() == 0 {
								return errors.New("file path must be provided")
							}

							if err := database.Connect(); err != nil {
								log.Fatal().Err(err).Msg("Failed to connect to database")
							}

							filePath := c.Args().Get(0)
							naptanDoc, err := naptan.ParseXMLFile(filePath, naptan.BusFilter)

							if err != nil {
								return err
							}

							naptanDoc.ImportIntoMongoAsCTDF()

							return nil
						},
					},
				},
			},
			{
				Name:  "transxchange",
				Usage: "TransXChange bus route data",
				Subcommands: []*cli.Command{
					{
						Name:      "import-file",
						Usage:     "import an XML file",
						ArgsUsage: "<file path>",
						Action: func(c *cli.Context) error {
							if c.Args().Len() == 0 {
								return errors.New("file path must be provided")
							}

							log.Print("hello world")

							// if err := database.Connect(); err != nil {
							// 	log.Fatal().Err(err).Msg("Failed to connect to database")
							// }

							filePath := c.Args().Get(0)
							transXChangeDoc, err := transxchange.ParseXMLFile(filePath)

							if err != nil {
								return err
							}

							transXChangeDoc.ImportIntoMongoAsCTDF()

							return nil
						},
					},
				},
			},
		},
	}

	err := app.Run(os.Args)
	if err != nil {
		log.Fatal().Err(err).Send()
	}
}
