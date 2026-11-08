package manager

import (
	"fmt"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func GetRegisteredDataSources() []datasets.DataSource {
	return datasets.LoadDataSources()
}

func GetRegisteredDataSets() []datasets.DataSet {
	var registeredDatasets []datasets.DataSet

	registeredDatasources := GetRegisteredDataSources()

	for _, datasource := range registeredDatasources {
		for _, dataset := range datasource.Datasets {
			dataset.OperatorAliases = datasource.Overrides.AliasesFor(dataset.Identifier)
			dataset.OperatorMetadata = datasource.Overrides.OperatorMetadataFor(dataset.Identifier)
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
