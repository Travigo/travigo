package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
		Name:        "data-importer",
		Description: "Manages ingesting and verifying data in BritBus",
		Commands: []*cli.Command{
			{
				Name:      "import",
				Usage:     "Import a dataset into BritBus",
				ArgsUsage: "<format> <source>",
				Action: func(c *cli.Context) error {
					if c.Args().Len() != 2 {
						return errors.New("<format> and <source> must be provided")
					}

					format := c.Args().Get(0)
					source := c.Args().Get(1)

					dataFiles := []DataFile{}

					// Check if the source is a URL and load the http client stream if it is
					if isValidUrl(source) {
						pretty.Println("This is a HTTP file")

						resp, err := http.Get(source)

						if err != nil {
							log.Fatal().Err(err)
						}
						defer resp.Body.Close()

						dataFiles = append(dataFiles, DataFile{
							Name:   source,
							Reader: resp.Body,
						})
					}

					// Ccheck if its an XML file or ZIP file
					fileExtension := filepath.Ext(source)
					if fileExtension == ".xml" {
						file, err := os.Open(source)
						if err != nil {
							log.Fatal().Err(err)
						}
						defer file.Close()

						dataFiles = append(dataFiles, DataFile{
							Name:   source,
							Reader: file,
						})
					} else if fileExtension == ".zip" {
						archive, err := zip.OpenReader(source)
						if err != nil {
							panic(err)
						}
						defer archive.Close()

						for _, zipFile := range archive.File {
							file, _ := zipFile.Open()

							dataFiles = append(dataFiles, DataFile{
								Name:   fmt.Sprintf("%s:%s", source, zipFile.Name),
								Reader: file,
							})
						}
					} else {
						return errors.New(fmt.Sprintf("Unsupport file extension %s", fileExtension))
					}

					for _, dataFile := range dataFiles {
						switch format {
						case "naptan":
							log.Info().Msgf("NaPTAN file import from %s", dataFile.Name)
							naptanDoc, err := naptan.ParseXMLFile(dataFile.Reader, naptan.BusFilter)

							if err != nil {
								return err
							}

							naptanDoc.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Provider: "Department of Transport",
								Dataset:  dataFile.Name,
							})
						case "traveline-noc":
							log.Info().Msgf("Traveline NOC file import from %s", dataFile.Name)
							travelineData, err := travelinenoc.ParseXMLFile(dataFile.Reader)

							if err != nil {
								return err
							}

							travelineData.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Dataset: dataFile.Name,
							})
						case "transxchange":
							log.Info().Msgf("TransXChange file import from %s ", dataFile.Name)
							transXChangeDoc, err := transxchange.ParseXMLFile(dataFile.Reader)

							if err != nil {
								return err
							}

							transXChangeDoc.ImportIntoMongoAsCTDF(&ctdf.DataSource{
								Provider: "Department of Transport", // This may not always be true
								Dataset:  dataFile.Name,
							})
						default:
							return errors.New(fmt.Sprintf("Unsupport format %s", format))
						}
					}

					return nil
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

func isValidUrl(toTest string) bool {
	_, err := url.ParseRequestURI(toTest)
	if err != nil {
		return false
	}

	u, err := url.Parse(toTest)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}

	return true
}
