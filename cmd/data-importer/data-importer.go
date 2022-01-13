package main

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/britbus/britbus/pkg/bods"
	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/britbus/britbus/pkg/database"
	"github.com/britbus/britbus/pkg/naptan"
	"github.com/britbus/britbus/pkg/transxchange"
	travelinenoc "github.com/britbus/britbus/pkg/traveline_noc"
	"github.com/britbus/notify/pkg/notify_client"
	"github.com/urfave/cli/v2"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type DataFile struct {
	Name   string
	Reader io.Reader
}

func importFile(dataFormat string, source string) error {
	dataFiles := []DataFile{}
	fileExtension := filepath.Ext(source)

	// Check if the source is a URL and load the http client stream if it is
	if isValidUrl(source) {
		resp, err := http.Get(source)

		if err != nil {
			log.Fatal().Err(err)
		}
		defer resp.Body.Close()

		_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
		if err == nil {
			fileExtension = filepath.Ext(params["filename"])
		} else {
			fileExtension = filepath.Ext(source)
		}

		tmpFile, err := ioutil.TempFile(os.TempDir(), "britbus-data-importer-")
		if err != nil {
			log.Fatal().Err(err).Msg("Cannot create temporary file")
		}
		defer os.Remove(tmpFile.Name())

		io.Copy(tmpFile, resp.Body)

		source = tmpFile.Name()
	}

	// Check if its an XML file or ZIP file

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
			file, err := zipFile.Open()
			if err != nil {
				log.Fatal().Err(err)
			}
			defer file.Close()

			dataFiles = append(dataFiles, DataFile{
				Name:   fmt.Sprintf("%s:%s", source, zipFile.Name),
				Reader: file,
			})
		}
	} else {
		return errors.New(fmt.Sprintf("Unsupported file extension %s", fileExtension))
	}

	for _, dataFile := range dataFiles {
		parseDataFile(dataFormat, &dataFile)
	}

	return nil
}

func parseDataFile(dataFormat string, dataFile *DataFile) error {
	switch dataFormat {
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
		return errors.New(fmt.Sprintf("Unsupported data-format %s", dataFormat))
	}

	return nil
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
				ArgsUsage: "<data-format> <source-format> <source>",
				Action: func(c *cli.Context) error {
					if c.Args().Len() != 3 {
						return errors.New("<data-format>, <source-format>, and <source> must be provided")
					}

					dataFormat := c.Args().Get(0)
					sourceFormat := c.Args().Get(1)
					source := c.Args().Get(2)

					if sourceFormat == "file" {
						err := importFile(dataFormat, source)

						return err
					} else if sourceFormat == "api" {
						switch dataFormat {
						case "transxchange":
							log.Info().Msgf("TransXChange API import from %s ", source)
							timeTableDataset, err := bods.GetTimetableDataset(source)
							log.Info().Msgf(" - %d datasets", len(timeTableDataset))

							if err != nil {
								return err
							}

							for _, dataset := range timeTableDataset {
								err = importFile(dataFormat, dataset.URL)

								if err != nil {
									log.Error().Err(err).Msgf("Failed to import file %s (%s)", dataset.Name, dataset.URL)
								}
							}
						default:
							return errors.New(fmt.Sprintf("Unsupported api data-format %s", dataFormat))
						}

					} else {
						return errors.New(fmt.Sprintf("Unsupported source-format %s", sourceFormat))
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
