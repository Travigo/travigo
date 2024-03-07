package dataimporter

import (
	"archive/zip"
	"compress/gzip"
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
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/dataimporter/cif"
	"github.com/travigo/travigo/pkg/dataimporter/gtfs"
	"github.com/travigo/travigo/pkg/dataimporter/insertrecords"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
	"github.com/travigo/travigo/pkg/dataimporter/nationalrailtoc"
	networkrailcorpus "github.com/travigo/travigo/pkg/dataimporter/networkrail-corpus"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/siri_vm"
	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/redis_client"
	"github.com/travigo/travigo/pkg/util"
	"github.com/urfave/cli/v2"
	"go.mongodb.org/mongo-driver/bson"

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
					insertrecords.Insert()

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
					if dataFormat == "siri-vm" || dataFormat == "gtfs-rt" {
						if err := redis_client.Connect(); err != nil {
							log.Fatal().Err(err).Msg("Failed to connect to Redis")
						}

						var err error
						realtimeQueue, err = redis_client.QueueConnection.OpenQueue("realtime-queue")
						if err != nil {
							log.Fatal().Err(err).Msg("Failed to start siri-vm redis queue")
						}

						// Get the API key from the environment variables and append to the source URL
						env := util.GetEnvironmentVariables()
						if env["TRAVIGO_BODS_API_KEY"] != "" && isValidUrl(source) {
							source += fmt.Sprintf("?api_key=%s", env["TRAVIGO_BODS_API_KEY"])
						}
					}

					for {
						startTime := time.Now()

						var datasource *ctdf.DataSource

						switch dataFormat {
						case "traveline-noc":
							datasource = &ctdf.DataSource{
								OriginalFormat: "traveline-noc",
								DatasetID:      source,
								Timestamp:      time.Now().Format(time.RFC3339),
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
				Name:  "nationalrail-timetable",
				Usage: "Import Train Operating Companies from the National Rail Open Data API",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "nationalrailbundle",
						Usage:    "Overwrite URL",
						Required: false,
					},
					&cli.StringFlag{
						Name:     "networkrailbundle",
						Usage:    "Overwrite URL",
						Required: false,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()
					insertrecords.Insert()

					// cifBundleTest := cif.CommonInterfaceFormat{}

					// testFile, err := os.Open("/Users/aaronclaydon/Downloads/tynewear.cif")

					// cifBundleTest.ParseMCA(testFile)

					// journeys := cifBundleTest.ConvertToCTDF()

					// for _, journey := range journeys {
					// 	pretty.Println(journey.PrimaryIdentifier)
					// 	pretty.Println(len(journey.Path))
					// }

					// return nil

					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NETWORKRAIL_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_USERNAME must be set")
					}
					if env["TRAVIGO_NETWORKRAIL_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_PASSWORD must be set")
					}

					nationalRailBundleSource := c.String("nationalrailbundle")
					if nationalRailBundleSource == "" {
						nationalRailBundleSource = "https://opendata.nationalrail.co.uk/api/staticfeeds/3.0/timetable"
					}
					networkRailBundleSource := c.String("networkrailbundle")
					if networkRailBundleSource == "" {
						networkRailBundleSource = "https://publicdatafeeds.networkrail.co.uk/ntrod/CifFileAuthenticate?type=CIF_ALL_FULL_DAILY&day=toc-full.CIF.gz"
					}

					log.Info().Msgf("National Rail Timetable import from %s", nationalRailBundleSource)

					if isValidUrl(nationalRailBundleSource) {
						token := nationalRailLogin()
						tempFile, _ := tempDownloadFile(nationalRailBundleSource, []string{
							"X-Auth-Token", token,
						})

						nationalRailBundleSource = tempFile.Name()
						defer os.Remove(tempFile.Name())
					}

					cifBundle, err := cif.ParseNationalRailCifBundle(nationalRailBundleSource, false)

					if err != nil {
						return err
					}

					req, _ := http.NewRequest("GET", networkRailBundleSource, nil)
					req.SetBasicAuth(env["TRAVIGO_NETWORKRAIL_USERNAME"], env["TRAVIGO_NETWORKRAIL_PASSWORD"])

					client := &http.Client{}
					resp, err := client.Do(req)

					nrbGzip, err := gzip.NewReader(resp.Body)
					if err != nil {
						log.Fatal().Err(err).Msg("cannot decode gzip stream")
					}
					log.Info().Msgf("Network Rail Timetable import from %s", networkRailBundleSource)
					cifBundle.ParseMCA(nrbGzip)
					resp.Body.Close()
					nrbGzip.Close()

					// Cleanup right at the begining once, as we do it as 1 big import
					datasource := &ctdf.DataSource{
						OriginalFormat: "CIF",
						Provider:       "GB-NationalRail",
						DatasetID:      "timetable",
						Timestamp:      time.Now().Format(time.RFC3339),
					}
					cleanupOldRecords("journeys", datasource)

					cifBundle.ImportIntoMongoAsCTDF(datasource)

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
					insertrecords.Insert()

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
						Provider:       "GB-NationalRail",
						DatasetID:      "TOC",
						Timestamp:      currentTime,
					}
					cleanupOldRecords("operators", datasource)
					cleanupOldRecords("operator_groups", datasource)

					if isValidUrl(source) {
						token := nationalRailLogin()
						tempFile, _ := tempDownloadFile(source, []string{
							"X-Auth-Token", token,
						})

						source = tempFile.Name()
						defer os.Remove(tempFile.Name())
					}

					err := importFile("nationalrail-toc", ctdf.TransportTypeRail, source, "xml", datasource, map[string]string{})
					if err != nil {
						log.Fatal().Err(err).Msg("Cannot import National Rail TOC document")
					}

					return nil
				},
			},
			{
				Name:  "networkrail-corpus",
				Usage: "Import STANOX Stop IDs to Stops from Network Rail CORPUS dataset",
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
					insertrecords.Insert()

					env := util.GetEnvironmentVariables()
					if env["TRAVIGO_NETWORKRAIL_USERNAME"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_USERNAME must be set")
					}
					if env["TRAVIGO_NETWORKRAIL_PASSWORD"] == "" {
						log.Fatal().Msg("TRAVIGO_NETWORKRAIL_PASSWORD must be set")
					}

					source := c.String("url")
					if source == "" {
						source = "https://publicdatafeeds.networkrail.co.uk/ntrod/SupportingFileAuthenticate?type=CORPUS"
					}

					log.Info().Msgf("Network Rail CORPUS import from %s", source)

					req, _ := http.NewRequest("GET", source, nil)
					req.SetBasicAuth(env["TRAVIGO_NETWORKRAIL_USERNAME"], env["TRAVIGO_NETWORKRAIL_PASSWORD"])

					client := &http.Client{}
					resp, err := client.Do(req)

					gzipDecoder, err := gzip.NewReader(resp.Body)
					if err != nil {
						log.Fatal().Err(err).Msg("cannot decode gzip stream")
					}
					defer gzipDecoder.Close()

					if err != nil {
						log.Fatal().Err(err).Msg("Download file")
					}
					defer resp.Body.Close()

					corpus, err := networkrailcorpus.ParseJSONFile(gzipDecoder)

					if err != nil {
						return err
					}

					// Cleanup right at the begining once, as we do it as 1 big import
					datasource := &ctdf.DataSource{
						OriginalFormat: "JSON-CORPUS",
						Provider:       "GB-NetworkRail",
						DatasetID:      source,
						Timestamp:      "",
					}

					corpus.ImportIntoMongoAsCTDF(datasource)

					return nil
				},
			},
			{
				Name:  "gtfs-timetable",
				Usage: "Import a GTFS Timetable",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "url",
						Usage:    "Path to the GTFS zip file",
						Required: true,
					},
					&cli.StringFlag{
						Name:     "datasetid",
						Usage:    "ID of the dataset",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()
					insertrecords.Insert()

					source := c.String("url")
					datasetid := c.String("datasetid")

					log.Info().Msgf("GTFS bundle import from %s", source)

					if isValidUrl(source) {
						tempFile, _ := tempDownloadFile(source)

						source = tempFile.Name()
						defer os.Remove(tempFile.Name())
					}

					gtfsFile, err := gtfs.ParseScheduleZip(source)

					if err != nil {
						return err
					}

					datasource := &ctdf.DataSource{
						OriginalFormat: "GTFS-SCHEDULE",
						Provider:       "Department of Transport (UK)",
						DatasetID:      datasetid,
						Timestamp:      fmt.Sprintf("%d", time.Now().Unix()),
					}

					gtfsFile.ImportIntoMongoAsCTDF(datasetid, datasource)

					cleanupOldRecords("services", datasource)
					cleanupOldRecords("journeys", datasource)

					return nil
				},
			},
			{
				Name:  "dataset",
				Usage: "Import a dataset",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:     "id",
						Usage:    "ID of the dataset",
						Required: true,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					ctdf.LoadSpecialDayCache()
					insertrecords.Insert()

					datasetid := c.String("id")

					err := manager.ImportDataset(datasetid)

					return err
				},
			},
		},
	}
}

func nationalRailLogin() string {
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

	return loginResponse.Token
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

	if fileExtension == ".xml" || fileExtension == ".cif" || fileExtension == ".json" || fileExtension == ".bundle" {
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
	case "siri-vm":
		log.Info().Msgf("Siri-VM file import from %s ", dataFile.Name)

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				Provider:  "Department of Transport", // This may not always be true
				DatasetID: dataFile.Name,
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
				Provider:  "National Rail", // This may not always be true
				DatasetID: dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		nationalRailTOCDoc.ImportIntoMongoAsCTDF(&datasource)
	case "networkrail-corpus":
		log.Info().Msgf("Network Rail Corpus file import from %s ", dataFile.Name)
		corpus, err := networkrailcorpus.ParseJSONFile(dataFile.Reader)

		if err != nil {
			return err
		}

		if sourceDatasource == nil {
			datasource = ctdf.DataSource{
				OriginalFormat: "JSON-CORPUS",
				Provider:       "GB-NetworkRail",
				DatasetID:      dataFile.Name,
			}
		} else {
			datasource = *sourceDatasource
		}

		corpus.ImportIntoMongoAsCTDF(&datasource)
	case "gtfs-rt":
		log.Info().Msgf("GTFS-RT file import from %s ", dataFile.Name)

		// TODO Def not always true
		datasource := &ctdf.DataSource{
			Provider:  "Department of Transport", // This may not always be true
			DatasetID: dataFile.Name,
		}

		err := gtfs.ParseRealtime(dataFile.Reader, realtimeQueue, datasource)

		if err != nil {
			return err
		}
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
			bson.M{"datasource.datasetid": datasource.DatasetID},
			bson.M{"datasource.timestamp": bson.M{
				"$ne": datasource.Timestamp,
			}},
		},
	}

	result, _ := collection.DeleteMany(context.Background(), query)

	if result != nil {
		log.Info().
			Str("collection", collectionName).
			Int64("num", result.DeletedCount).
			Msg("Cleaned up old records")
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
