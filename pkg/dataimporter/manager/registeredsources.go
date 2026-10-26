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
	// }
}
