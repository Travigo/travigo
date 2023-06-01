package dataimporter

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/bods"
	"github.com/travigo/travigo/pkg/dataimporter/naptan"
	"github.com/travigo/travigo/pkg/dataimporter/nationalrailtoc"
	"github.com/travigo/travigo/pkg/dataimporter/siri_vm"
	"github.com/travigo/travigo/pkg/dataimporter/transxchange"
	"github.com/travigo/travigo/pkg/dataimporter/travelinenoc"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/util"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/rs/zerolog/log"

	_ "time/tzdata"
)

var realtimeQueue rmq.Queue

type DataFile struct {
	Name      string
	Reader    io.Reader
	Overrides map[string]string

	TransportType ctdf.TransportType
}

func RegisterCLI() *cli.Command {
	return &cli.Command{
		Name:  "data-importer",
		Usage: "Download & convert third party datasets into CTDF",
		Subcommands: []*cli.Command{
			{
				Name:  "file",
				Usage: "Import a dataset into Travigo",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "repeat-every",
						Usage:    "Repeat this file import every X seconds",
						Required: false,
					},
					&cli.StringFlag{
						Name:     "file-format",
						Usage:    "Overwrite the file format (eg. zip or xml)",
						Required: false,
					},
					&cli.StringFlag{
						Name:     "transport-type",
						Usage:    "Set the transport type for this file",
						Required: false,
					},
				},
				ArgsUsage: "<data-format> <source>",
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := elastic_client.Connect(false); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()

					if c.Args().Len() != 2 {
						return errors.New("<data-format> and <source> must be provided")
					}

					fileFormat := c.String("file-format")
					transportType := c.String("transport-type")

					repeatEvery := c.String("repeat-every")
					repeat := repeatEvery != ""
					var repeatDuration time.Duration
					if repeat {
						var err error
						repeatDuration, err = time.ParseDuration(repeatEvery)

						if err != nil {
							return err
						}
					}

					dataFormat := c.Args().Get(0)
					source := c.Args().Get(1)

					// Some initial setup for Siri-VM
					if dataFormat == "siri-vm" {
						if err := redis_client.Connect(); err != nil {
							log.Fatal().Err(err).Msg("Failed to connect to Redis")
						}

						var err error
						realtimeQueue, err = redis_client.QueueConnection.OpenQueue("realtime-queue")
						if err != nil {
							log.Fatal().Err(err).Msg("Failed to start siri-vm redis queue")
						}

						//TODO: TEMPORARY
						// Get the API key from the environment variables and append to the source URL
						env := util.GetEnvironmentVariables()
						if env["TRAVIGO_BODS_API_KEY"] != "" {
							source += fmt.Sprintf("?api_key=%s", env["TRAVIGO_BODS_API_KEY"])
						}
					}

					for {
						startTime := time.Now()

						var datasource *ctdf.DataSource

						switch dataFormat {
						case "naptan":
							datasource = &ctdf.DataSource{
								OriginalFormat: "naptan",
								Dataset:        source,
								Identifier:     time.Now().Format(time.RFC3339),
							}

							cleanupOldRecords("stops", datasource)
							cleanupOldRecords("stop_groups", datasource)
						case "traveline-noc":
							datasource = &ctdf.DataSource{
								OriginalFormat: "traveline-noc",
								Dataset:        source,
								Identifier:     time.Now().Format(time.RFC3339),
							}

							cleanupOldRecords("operators", datasource)
							cleanupOldRecords("operator_groups", datasource)
						}

						err := importFile(dataFormat, ctdf.TransportType(transportType), source, fileFormat, datasource, map[string]string{})

						if err != nil {
							return err
						}

						if !repeat {
							break
						}

						executionDuration := time.Since(startTime)
						log.Info().Msgf("Operation took %s", executionDuration.String())

						waitTime := repeatDuration - executionDuration

						if waitTime.Seconds() > 0 {
							time.Sleep(waitTime)
						}
					}

					return nil
				},
			},
			{
				Name:  "bods-timetable",
				Usage: "Import TransXChange Timetable datasets from BODS into Travigo",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "url",
						Usage:    "Overwrite URL for the BODS Timetable API",
						Required: false,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()

					bodsDatasetIdentifier := "GB-DfT-BODS"

					source := c.String("url")

					// Default source of all published buses
					if source == "" {
						source = "https://data.bus-data.dft.gov.uk/api/v1/dataset/?limit=25&offset=0&status=published"
					}

					// First get the unique set of datasets that we have previously imported
					// This will be used later to see if any are no longer active in the dataset, and should be removed
					datasetVersionsCollection := database.GetCollection("dataset_versions")
					datasetVersionsIdentifiers, _ := datasetVersionsCollection.Distinct(context.Background(), "identifier", bson.M{"dataset": bodsDatasetIdentifier})
					datasetIdentifiersSeen := map[string]bool{}

					log.Info().Msgf("Bus Open Data Service Timetable API import from %s ", source)

					// Get the API key from the environment variables and append to the source URL
					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_BODS_API_KEY"] != "" {
						source += fmt.Sprintf("&api_key=%s", env["TRAVIGO_BODS_API_KEY"])
					}

					timeTableDataset, err := bods.GetTimetableDataset(source)
					log.Info().Msgf(" - %d datasets", len(timeTableDataset))

					if err != nil {
						return err
					}

					for _, dataset := range timeTableDataset {
						datasetIdentifiersSeen[fmt.Sprint(dataset.ID)] = true

						var datasetVersion *ctdf.DatasetVersion

						query := bson.M{"$and": bson.A{
							bson.M{"dataset": bodsDatasetIdentifier},
							bson.M{"identifier": fmt.Sprintf("%d", dataset.ID)},
						}}
						datasetVersionsCollection.FindOne(context.Background(), query).Decode(&datasetVersion)

						if datasetVersion == nil || datasetVersion.LastModified != dataset.Modified {
							datasource := &ctdf.DataSource{
								OriginalFormat: "transxchange",
								Provider:       bodsDatasetIdentifier,
								Dataset:        fmt.Sprint(dataset.ID),
								Identifier:     dataset.Modified,
							}

							// Cleanup per file if it changes
							cleanupOldRecords("services", datasource)
							cleanupOldRecords("journeys", datasource)

							err = importFile("transxchange", ctdf.TransportTypeBus, dataset.URL, "", datasource, map[string]string{})

							if err != nil {
								log.Error().Err(err).Msgf("Failed to import file %s (%s)", dataset.Name, dataset.URL)
								continue
							}

							if datasetVersion == nil {
								datasetVersion = &ctdf.DatasetVersion{
									Dataset:    bodsDatasetIdentifier,
									Identifier: fmt.Sprintf("%d", dataset.ID),
								}
							}
							datasetVersion.LastModified = dataset.Modified

							opts := options.Update().SetUpsert(true)
							datasetVersionsCollection.UpdateOne(context.Background(), query, bson.M{"$set": datasetVersion}, opts)
						} else {
							log.Info().Int("id", dataset.ID).Msg("Dataset not changed")
						}
					}

					// Now check if this new results has any datasets removed
					for _, datasetIdentifier := range datasetVersionsIdentifiers {
						if !datasetIdentifiersSeen[datasetIdentifier.(string)] {
							log.Info().Str("id", datasetIdentifier.(string)).Msg("Deleted removed dataset")

							datasetVersionsCollection.DeleteMany(context.Background(), bson.M{"$and": bson.A{
								bson.M{"dataset": bodsDatasetIdentifier},
								bson.M{"identifier": datasetIdentifier.(string)},
							}})

							journeysCollection := database.GetCollection("journeys")
							servicesCollection := database.GetCollection("services")

							query := bson.M{
								"$and": bson.A{
									bson.M{"datasource.originalformat": "transxchange"},
									bson.M{"datasource.provider": bodsDatasetIdentifier},
									bson.M{"datasource.dataset": datasetIdentifier.(string)},
								},
							}

							journeysCollection.DeleteMany(context.Background(), query)
							servicesCollection.DeleteMany(context.Background(), query)
						}
					}

					return nil
				},
			},
			{
				Name:  "tfl",
				Usage: "Import Transport for London (TfL) datasets from their API into Travigo",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "url",
						Usage:    "Overwrite URL for the TfL TransXChange file",
						Required: false,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()

					source := c.String("url")

					// Default source of all published buses
					if source == "" {
						source = "https://tfl.gov.uk/tfl/syndication/feeds/journey-planner-timetables.zip"
					}

					currentTime := time.Now().Format(time.RFC3339)

					log.Info().Msgf("TfL TransXChange bundle import from %s", source)

					if isValidUrl(source) {
						tempFile, _ := tempDownloadFile(source)

						source = tempFile.Name()
						defer os.Remove(tempFile.Name())
					}

					archive, err := zip.OpenReader(source)
					if err != nil {
						log.Fatal().Str("source", source).Err(err).Msg("Could not open zip file")
					}
					defer archive.Close()

					tflBusesMatchRegex, _ := regexp.Compile("(?i)BUSES PART \\w+ \\d+.zip")
					tflOtherMatchRegex, _ := regexp.Compile("(?i)LULDLRTRAMRIVERCABLE.*\\.zip")

					// Cleanup right at the begining once, as we do it as 1 big import
					datasource := &ctdf.DataSource{
						OriginalFormat: "transxchange",
						Provider:       "GB-TfL-Transxchange",
						Dataset:        "ALL",
						Identifier:     currentTime,
					}
					cleanupOldRecords("services", datasource)
					cleanupOldRecords("journeys", datasource)

					for _, zipFile := range archive.File {

						file, err := zipFile.Open()
						if err != nil {
							log.Fatal().Err(err).Msg("Failed to open file")
						}
						defer file.Close()

						tmpFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-tfl-innerzip-")
						if err != nil {
							log.Fatal().Err(err).Msg("Cannot create temporary file")
						}
						defer os.Remove(tmpFile.Name())

						io.Copy(tmpFile, file)

						var transportType ctdf.TransportType
						if tflBusesMatchRegex.MatchString(zipFile.Name) {
							transportType = ctdf.TransportTypeBus
						} else if tflOtherMatchRegex.MatchString(zipFile.Name) { // TODO: NOT TRUE
							transportType = ctdf.TransportTypeMetro
						}

						if transportType != "" {
							err = importFile("transxchange", transportType, tmpFile.Name(), "zip", datasource, map[string]string{
								"OperatorRef": "GB:NOC:TFLO",
							})
							if err != nil {
								log.Fatal().Err(err).Msg("Cannot import TfL inner zip")
							}
						}
					}

					// TfL love to split out services over many files with different IDs
					// Use the squash function to turn them into 1
					squashIdenticalServices(bson.M{
						"operatorref": "GB:NOC:TFLO",
					})

					return nil
				},
			},
			{
				Name:  "nationalrail-toc",
				Usage: "Import Train Operating Companies from the National Rail Open Data API",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "url",
						Usage:    "Overwrite URL",
						Required: false,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()

					source := c.String("url")

					// Default source of all published buses
					if source == "" {
						source = "https://opendata.nationalrail.co.uk/api/staticfeeds/4.0/tocs"
					}

					currentTime := time.Now().Format(time.RFC3339)

					log.Info().Msgf("National Rail TOC import from %s", source)

					// Cleanup right at the begining once, as we do it as 1 big import
					datasource := &ctdf.DataSource{
						OriginalFormat: "nationalrail-toc",
						Provider:       "GB-NationalRail-TOC",
						Dataset:        "ALL",
						Identifier:     currentTime,
					}
					cleanupOldRecords("operators", datasource)
					cleanupOldRecords("operator_groups", datasource)

					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NATIONALRAIL_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NATIONALRAIL_USERNAME must be set")
					}
					if env["TRAVIGO_NATIONALRAIL_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NATIONALRAIL_PASSWORD must be set")
					}

					formData := url.Values{
						"username": {env["TRAVIGO_NATIONALRAIL_USERNAME"]},
						"password": {env["TRAVIGO_NATIONALRAIL_PASSWORD"]},
					}

					client := &http.Client{}
					req, err := http.NewRequest("POST", "https://opendata.nationalrail.co.uk/authenticate", strings.NewReader(formData.Encode()))
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to create auth HTTP request")
					}

					req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

					resp, err := client.Do(req)
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to perform auth HTTP request")
					}
					defer resp.Body.Close()

					body, err := io.ReadAll(resp.Body)
					if err != nil {
						log.Fatal().Err(err).Msg("Failed to read auth HTTP request")
					}

					var loginResponse struct {
						Token string `json:"token"`
					}
					json.Unmarshal(body, &loginResponse)

					if isValidUrl(source) {
						tempFile, _ := tempDownloadFile(source, []string{
							"X-Auth-Token", loginResponse.Token,
						})

						source = tempFile.Name()
						defer os.Remove(tempFile.Name())
					}

					err = importFile("nationalrail-toc", ctdf.TransportTypeRail, source, "xml", datasource, map[string]string{})
					if err != nil {
						log.Fatal().Err(err).Msg("Cannot import National Rail TOC document")
					}

					return nil
				},
			},
		},
	}
}

func tempDownloadFile(source string, headers ...[]string) (*os.File, string) {
	req, _ := http.NewRequest("GET", source, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	for _, header := range headers {
		req.Header[header[0]] = []string{header[1]}
	}

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	_, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	fileExtension := filepath.Ext(source)
	if err == nil {
		fileExtension = filepath.Ext(params["filename"])
	}

	tmpFile, err := os.CreateTemp(os.TempDir(), "travigo-data-importer-")
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot create temporary file")
	}

	io.Copy(tmpFile, resp.Body)

	return tmpFile, fileExtension
}

func importFile(dataFormat string, transportType ctdf.TransportType, source string, fileFormat string, sourceDatasource *ctdf.DataSource, overrides map[string]string) error {
	var dataFiles []DataFile
	fileExtension := filepath.Ext(source)

	// Check if the source is a URL and load the http client stream if it is
	if isValidUrl(source) {
		var tempFile *os.File
		tempFile, fileExtension = tempDownloadFile(source)

		source = tempFile.Name()
		defer os.Remove(tempFile.Name())
	}

	if fileFormat != "" {
		fileExtension = fmt.Sprintf(".%s", fileFormat)
	}

	// Check if its an XML file or ZIP file

	if fileExtension == ".xml" {
		file, err := os.Open(source)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to open file")
		}
		defer file.Close()

		dataFiles = append(dataFiles, DataFile{
			Name:          source,
			Reader:        file,
			Overrides:     overrides,
			TransportType: transportType,
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
				log.Fatal().Err(err).Msg("Failed to open file")
			}
			defer file.Close()

			dataFiles = append(dataFiles, DataFile{
				Name:          fmt.Sprintf("%s:%s", source, zipFile.Name),
				Reader:        file,
				Overrides:     overrides,
				TransportType: transportType,
			})
		}
	} else {
		return errors.New(fmt.Sprintf("Unsupported file extension %s", fileExtension))
	}

	for _, dataFile := range dataFiles {
		err := parseDataFile(dataFormat, &dataFile, sourceDatasource)

		if err != nil {
			return err
		}
	}

	return nil
}

func parseDataFile(dataFormat string, dataFile *DataFile, sourceDatasource *ctdf.DataSource) error {
	var datasource ctdf.DataSource

	switch dataFormat {
	case "naptan":
		log.Info().Msgf("NaPTAN file import from %s", dataFile.Name)
		naptanDoc, err := naptan.ParseXMLFile(dataFile.Reader)

		if err != nil {
			return err
		}

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				Provider: "Department of Transport",
				Dataset:  dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		naptanDoc.ImportIntoMongoAsCTDF(&datasource)
	case "traveline-noc":
		log.Info().Msgf("Traveline NOC file import from %s", dataFile.Name)
		travelineData, err := travelinenoc.ParseXMLFile(dataFile.Reader)

		if err != nil {
			return err
		}

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				Dataset: dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		travelineData.ImportIntoMongoAsCTDF(&datasource)
	case "transxchange":
		log.Info().Msgf("TransXChange file import from %s ", dataFile.Name)
		transXChangeDoc, err := transxchange.ParseXMLFile(dataFile.Reader)

		if err != nil {
			return err
		}

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				Provider: "Department of Transport", // This may not always be true
				Dataset:  dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		transXChangeDoc.ImportIntoMongoAsCTDF(&datasource, dataFile.TransportType, dataFile.Overrides)
	case "siri-vm":
		log.Info().Msgf("Siri-VM file import from %s ", dataFile.Name)

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				Provider: "Department of Transport", // This may not always be true
				Dataset:  dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		err := siri_vm.ParseXMLFile(dataFile.Reader, realtimeQueue, &datasource)

		if err != nil {
			return err
		}
	case "nationalrail-toc":
		log.Info().Msgf("National Rail TOC file import from %s ", dataFile.Name)
		nationalRailTOCDoc, err := nationalrailtoc.ParseXMLFile(dataFile.Reader)

		if err != nil {
			return err
		}

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				Provider: "National Rail", // This may not always be true
				Dataset:  dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		nationalRailTOCDoc.ImportIntoMongoAsCTDF(&datasource)
	default:
		return errors.New(fmt.Sprintf("Unsupported data-format %s", dataFormat))
	}

	return nil
}

func cleanupOldRecords(collectionName string, datasource *ctdf.DataSource) {
	collection := database.GetCollection(collectionName)

	query := bson.M{
		"$and": bson.A{
			bson.M{"datasource.originalformat": datasource.OriginalFormat},
			bson.M{"datasource.provider": datasource.Provider},
			bson.M{"datasource.dataset": datasource.Dataset},
			bson.M{"datasource.identifier": bson.M{
				"$ne": datasource.Identifier,
			}},
		},
	}

	collection.DeleteMany(context.Background(), query)
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
