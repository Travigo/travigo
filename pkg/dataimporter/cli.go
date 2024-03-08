package dataimporter

import (
	"compress/gzip"
	"context"
	"encoding/json"
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
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs"
	"github.com/travigo/travigo/pkg/dataimporter/insertrecords"
	"github.com/travigo/travigo/pkg/dataimporter/manager"

	"github.com/adjust/rmq/v5"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
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

					cifBundle.Import(datasource)

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

					gtfsFile.Import(datasetid, datasource)

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
					&cli.StringFlag{
						Name:     "repeat-every",
						Usage:    "Repeat this file import every X seconds",
						Required: false,
					},
				},
				Action: func(c *cli.Context) error {
					if err := database.Connect(); err != nil {
						return err
					}
					if err := redis_client.Connect(); err != nil {
						log.Fatal().Err(err).Msg("Failed to connect to Redis")
					}
					ctdf.LoadSpecialDayCache()
					insertrecords.Insert()

					datasetid := c.String("id")

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

					dataset, err := manager.GetDataset(datasetid)
					if err != nil {
						return err
					}

					for {
						startTime := time.Now()

						err := dataset.ImportDataset()

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
