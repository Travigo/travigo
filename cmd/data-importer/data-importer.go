package main

import (
	"errors"
	"os"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/britbus/britbus/pkg/transxchange"
	travelinenoc "github.com/britbus/britbus/pkg/traveline_noc"
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

							log.Info().Msgf("NaPTAN file import from %s", filePath)

							naptanDoc, err := naptan.ParseXMLFile(filePath, naptan.BusFilter)

							log.Info().Msgf("Successfully parsed document")
							log.Info().Msgf(" - Last modified %s", naptanDoc.ModificationDateTime)
							log.Info().Msgf(" - Contains %d stops", len(naptanDoc.StopPoints))
							log.Info().Msgf(" - Contains %d stop areas", len(naptanDoc.StopAreas))

							if err != nil {
								return err
							}

							naptanDoc.ImportIntoMongoAsCTDF()

							log.Info().Msgf("Successfully imported into MongoDB")

							return nil
						},
					},
				},
			},
			{
				Name:  "traveline-noc",
				Usage: "Traveline National Operator Code data",
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

							log.Info().Msgf("Traveline NOC file import from %s", filePath)

							travelineData, err := travelinenoc.ParseXMLFile(filePath)

							log.Info().Msgf("Successfully parsed document")
							log.Info().Msgf(" - Contains %d NOCLinesRecords", len(travelineData.NOCLinesRecords))
							log.Info().Msgf(" - Contains %d NOCTableRecords", len(travelineData.NOCTableRecords))
							log.Info().Msgf(" - Contains %d OperatorRecords", len(travelineData.OperatorsRecords))
							log.Info().Msgf(" - Contains %d GroupsRecords", len(travelineData.GroupsRecords))
							log.Info().Msgf(" - Contains %d ManagementDivisionsRecords", len(travelineData.ManagementDivisionsRecords))
							log.Info().Msgf(" - Contains %d PublicNameRecords", len(travelineData.PublicNameRecords))

							if err != nil {
								return err
							}

							travelineData.ImportIntoMongoAsCTDF()

							log.Info().Msgf("Successfully imported into MongoDB")

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

							// if err := database.Connect(); err != nil {
							// 	log.Fatal().Err(err).Msg("Failed to connect to database")
							// }

							filePath := c.Args().Get(0)

							log.Info().Msgf("TransXChange file import from %s", filePath)

							transXChangeDoc, err := transxchange.ParseXMLFile(filePath)

							log.Info().Msgf("Successfully parsed document")
							log.Info().Msgf(" - Last modified %s", transXChangeDoc.ModificationDateTime)
							log.Info().Msgf(" - Contains %d operators", len(transXChangeDoc.Operators))
							log.Info().Msgf(" - Contains %d services", len(transXChangeDoc.Services))
							log.Info().Msgf(" - Contains %d routes", len(transXChangeDoc.Routes))
							log.Info().Msgf(" - Contains %d route sections", len(transXChangeDoc.RouteSections))
							log.Info().Msgf(" - Contains %d vehicle journeys", len(transXChangeDoc.VehicleJourneys))

							if err != nil {
								return err
							}

							transXChangeDoc.ImportIntoMongoAsCTDF()

							log.Info().Msgf("Successfully imported into MongoDB")

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
