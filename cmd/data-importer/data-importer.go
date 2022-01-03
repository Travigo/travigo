package main

import (
	"errors"
	"net/http"
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

	if err := database.Connect(); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}

	app := &cli.App{
		Name: "data-importer",
		Commands: []*cli.Command{
			{
				Name:  "naptan",
				Usage: "NaPTAN stop location data",
				Subcommands: []*cli.Command{
					{
						Name:      "import-file",
						Usage:     "import a local XML file",
						ArgsUsage: "<file path>",
						Action: func(c *cli.Context) error {
							if c.Args().Len() == 0 {
								return errors.New("file path must be provided")
							}

							filePath := c.Args().Get(0)

							log.Info().Msgf("NaPTAN file import from %s", filePath)

							file, err := os.Open(filePath)
							if err != nil {
								log.Fatal().Err(err)
							}
							defer file.Close()

							naptanDoc, err := naptan.ParseXMLFile(file, naptan.BusFilter)

							if err != nil {
								return err
							}

							naptanDoc.ImportIntoMongoAsCTDF()

							return nil
						},
					},
					{
						Name:      "import-http",
						Usage:     "import a remote XML file over HTTP",
						ArgsUsage: "<file path>",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "url",
								Value: "https://naptan.api.dft.gov.uk/v1/access-nodes?dataFormat=xml",
								Usage: "HTTP location of the XML file",
							},
						},
						Action: func(c *cli.Context) error {
							url := c.String("url")
							log.Info().Msgf("NaPTAN file import from %s", url)

							resp, err := http.Get(url)

							if err != nil {
								log.Fatal().Err(err)
							}
							defer resp.Body.Close()

							naptanDoc, err := naptan.ParseXMLFile(resp.Body, naptan.BusFilter)

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
				Name:  "traveline-noc",
				Usage: "Traveline National Operator Code data",
				Subcommands: []*cli.Command{
					{
						Name:      "import-file",
						Usage:     "import a local XML file",
						ArgsUsage: "<file path>",
						Action: func(c *cli.Context) error {
							if c.Args().Len() == 0 {
								return errors.New("file path must be provided")
							}

							filePath := c.Args().Get(0)

							log.Info().Msgf("Traveline NOC file import from %s", filePath)

							file, err := os.Open(filePath)
							if err != nil {
								log.Fatal().Err(err)
							}
							defer file.Close()

							travelineData, err := travelinenoc.ParseXMLFile(file)

							if err != nil {
								return err
							}

							travelineData.ImportIntoMongoAsCTDF()

							return nil
						},
					},
					{
						Name:  "import-http",
						Usage: "import a remote XML file over HTTP",
						Flags: []cli.Flag{
							&cli.StringFlag{
								Name:  "url",
								Value: "https://www.travelinedata.org.uk/noc/api/1.0/nocrecords.xml",
								Usage: "HTTP location of the XML file",
							},
						},
						Action: func(c *cli.Context) error {
							url := c.String("url")
							log.Info().Msgf("Traveline NOC HTTP import from %s", url)

							resp, err := http.Get(url)

							if err != nil {
								log.Fatal().Err(err)
							}
							defer resp.Body.Close()

							travelineData, err := travelinenoc.ParseXMLFile(resp.Body)

							if err != nil {
								return err
							}

							travelineData.ImportIntoMongoAsCTDF()

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
