package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/britbus/britbus/pkg/transxchange"
	travelinenoc "github.com/britbus/britbus/pkg/traveline_noc"
	"github.com/britbus/notify/pkg/notify_client"
	"github.com/kr/pretty"
	"github.com/urfave/cli/v2"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type DataFile struct {
	Name   string
	Reader io.Reader
}

func main() {
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339})

	if err := database.Connect(); err != nil {
		log.Fatal().Err(err).Msg("Failed to connect to database")
	}

	// Setup the notifications client
	notify_client.Setup()

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

							naptanDoc.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Provider: "Department of Transport",
								Dataset:  fmt.Sprintf("local-file:%s", filePath),
							})

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
							log.Info().Msgf("NaPTAN HTTP import from %s", url)

							resp, err := http.Get(url)

							if err != nil {
								log.Fatal().Err(err)
							}
							defer resp.Body.Close()

							naptanDoc, err := naptan.ParseXMLFile(resp.Body, naptan.BusFilter)

							if err != nil {
								return err
							}

							naptanDoc.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Provider: "Department of Transport",
								Dataset:  url,
							})

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

							travelineData.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Dataset: fmt.Sprintf("local-file:%s", filePath),
							})

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

							travelineData.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Dataset: url,
							})

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

							filePath := c.Args().Get(0)
							fileExtension := filepath.Ext(filePath)

							files := []DataFile{}

							if fileExtension == ".xml" {
								pretty.Println("lovely xml file")

								file, err := os.Open(filePath)
								if err != nil {
									log.Fatal().Err(err)
								}
								defer file.Close()

								files = append(files, DataFile{
									Name:   "",
									Reader: file,
								})
							} else if fileExtension == ".zip" {
								pretty.Println("lovely zip file")

								archive, err := zip.OpenReader(filePath)
								if err != nil {
									panic(err)
								}
								defer archive.Close()

								for _, zipFile := range archive.File {
									file, _ := zipFile.Open()

									files = append(files, DataFile{
										Name:   zipFile.Name,
										Reader: file,
									})
								}
							} else {
								return errors.New(fmt.Sprintf("Unsupport file extension %s", fileExtension))
							}

							for _, file := range files {
								log.Info().Msgf("TransXChange file import from %s %s", filePath, file.Name)

								transXChangeDoc, err := transxchange.ParseXMLFile(file.Reader)

								if err != nil {
									return err
								}

								transXChangeDoc.ImportIntoMongoAsCTDF(&ctdf.DataSource{
									Dataset: fmt.Sprintf("local-file:%s %s", filePath, file.Name),
								})
							}

							return nil
						},
					},
				},
			},
		},
	}

	err := app.Run(os.Args)

	notify_client.Await()

	if err != nil {
		log.Fatal().Err(err).Send()
	}
}
