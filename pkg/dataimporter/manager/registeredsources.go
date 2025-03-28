package manager

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"gopkg.in/yaml.v3"
)

func GetRegisteredDataSources() []datasets.DataSource {
	var registeredDatasources []datasets.DataSource

	err := filepath.Walk("data/datasources/",
		func(path string, fileInfo os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			if !fileInfo.IsDir() {
				log.Debug().Str("path", path).Msg("Loading transforms file")

				extension := filepath.Ext(path)

				if extension != ".yaml" {
					return nil
				}

				transformYaml, err := os.ReadFile(path)
				if err != nil {
					return err
				}

				decoder := yaml.NewDecoder(bytes.NewReader(transformYaml))

				for {
					var datasource datasets.DataSource
					if decoder.Decode(&datasource) != nil {
						break
					}

					registeredDatasources = append(registeredDatasources, datasource)
				}
			}

			return nil
		})
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to load datasources directory")
	}

	return registeredDatasources
}

func GetRegisteredDataSets() []datasets.DataSet {
	var registeredDatasets []datasets.DataSet

	registeredDatasources := GetRegisteredDataSources()

	for _, datasource := range registeredDatasources {
		for _, dataset := range datasource.Datasets {
			dataset.Identifier = fmt.Sprintf("%s-%s", datasource.Identifier, dataset.Identifier)
			dataset.DataSourceRef = datasource.Identifier
			dataset.Provider = datasource.Provider

			if dataset.SourceAuthentication == nil && datasource.SourceAuthentication != nil {
				dataset.SourceAuthentication = datasource.SourceAuthentication
			}

			if (dataset.SupportedObjects == datasets.SupportedObjects{}) {
				dataset.SupportedObjects = datasets.SupportedObjects{
					Operators:      true,
					OperatorGroups: true,
					Stops:          true,
					StopGroups:     true,
					Services:       true,
					Journeys:       true,

					RealtimeJourneys: true,
					ServiceAlerts:    true,
				}
			}

			registeredDatasets = append(registeredDatasets, dataset)
		}
	}

	return registeredDatasets

	// return []datasets.DataSet{
	// 	{
	// 		Identifier: "us-nyc-subway-schedule",
	// 		Format:     datasets.DataSetFormatGTFSSchedule,
	// 		Provider: datasets.Provider{
	// 			Name:    "Metropolitan Transportation Authority",
	// 			Website: "https://mta.info",
	// 		},
	// 		Source:       "http://web.mta.info/developers/data/nyct/subway/google_transit.zip",
	// 		UnpackBundle: datasets.BundleFormatNone,
	// 		SupportedObjects: datasets.SupportedObjects{
	// 			Operators: true,
	// 			Stops:     true,
	// 			Services:  true,
	// 			Journeys:  true,
	// 		},
	// 	},
	// {
	// 	Identifier: "us-nyc-subway-relatime-1-2-3-4-5-6-7",
	// 	Format:     datasets.DataSetFormatGTFSRealtime,
	// 	Provider: datasets.Provider{
	// 		Name:    "Metropolitan Transportation Authority",
	// 		Website: "https://mta.info",
	// 	},
	// 	Source:       "https://api-endpoint.mta.info/Dataservice/mtagtfsfeeds/nyct%2Fgtfs",
	// 	UnpackBundle: datasets.BundleFormatNone,
	// 	SupportedObjects: datasets.SupportedObjects{
	// 		RealtimeJourneys: true,
	// 	},
	// 	ImportDestination: datasets.ImportDestinationRealtimeQueue,
	// 	LinkedDataset:     "us-nyc-subway-schedule",

	// 	DownloadHandler: func(r *http.Request) {
	// 		env := util.GetEnvironmentVariables()
	// 		if env["TRAVIGO_US_NYC_MTA_API_KEY"] == "" {
	// 			log.Fatal().Msg("TRAVIGO_US_NYC_MTA_API_KEY must be set")
	// 		}

	// 		r.Header.Set("x-api-key", env["TRAVIGO_US_NYC_MTA_API_KEY"])
	// 	},
	// },
	// 	{
	// 		Identifier: "eu-flixbus-gtfs-schedule",
	// 		Format:     datasets.DataSetFormatGTFSSchedule,
	// 		Provider: datasets.Provider{
	// 			Name:    "FlixBus",
	// 			Website: "https://global.flixbus.com",
	// 		},
	// 		Source:       "http://gtfs.gis.flix.tech/gtfs_generic_eu.zip",
	// 		UnpackBundle: datasets.BundleFormatNone,
	// 		SupportedObjects: datasets.SupportedObjects{
	// 			Operators: false,
	// 			Stops:     false,
	// 			Services:  false,
	// 			Journeys:  true,
	// 		},
	// 	},
	// 	{
	// 		Identifier: "us-bart-gtfs-schedule",
	// 		Format:     datasets.DataSetFormatGTFSSchedule,
	// 		Provider: datasets.Provider{
	// 			Name:    "BART",
	// 			Website: "http://www.bart.gov",
	// 		},
	// 		Source:       "https://www.bart.gov/dev/schedules/google_transit.zip",
	// 		UnpackBundle: datasets.BundleFormatNone,
	// 		SupportedObjects: datasets.SupportedObjects{
	// 			Operators: true,
	// 			Stops:     true,
	// 			Services:  true,
	// 			Journeys:  true,
	// 		},
	// 	},
	// 	{
	// 		Identifier: "us-bart-gtfs-realtime",
	// 		Format:     datasets.DataSetFormatGTFSRealtime,
	// 		Provider: datasets.Provider{
	// 			Name:    "BART",
	// 			Website: "http://www.bart.gov",
	// 		},
	// 		Source:       "https://api.bart.gov/gtfsrt/tripupdate.aspx", // https://api.bart.gov/gtfsrt/alerts.aspx
	// 		UnpackBundle: datasets.BundleFormatNone,
	// 		SupportedObjects: datasets.SupportedObjects{
	// 			RealtimeJourneys: true,
	// 		},
	// 		ImportDestination: datasets.ImportDestinationRealtimeQueue,
	// 		LinkedDataset:     "us-bart-gtfs-schedule",
	// 	},
	// }
}
