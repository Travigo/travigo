package manager

import "github.com/travigo/travigo/pkg/dataimporter/formats"

// Just a static list for now
func GetRegisteredDataSets() []DataSet {
	return []DataSet{
		{
			Identifier: "gb-traveline-noc",
			Format:     DataSetFormatTravelineNOC,
			Provider: Provider{
				Name:    "Traveline",
				Website: "https://www.travelinedata.org.uk/",
			},
			Source:       "https://www.travelinedata.org.uk/noc/api/1.0/nocrecords.xml",
			UnpackBundle: BundleFormatNone,
			SupportedObjects: formats.SupportedObjects{
				Operators:      true,
				OperatorGroups: true,
			},
		},
		{
			Identifier: "gb-dft-naptan",
			Format:     DataSetFormatNaPTAN,
			Provider: Provider{
				Name:    "Department for Transport",
				Website: "https://www.gov.uk/government/organisations/department-for-transport",
			},
			Source:       "https://naptan.api.dft.gov.uk/v1/access-nodes?dataFormat=xml",
			UnpackBundle: BundleFormatNone,
			SupportedObjects: formats.SupportedObjects{
				Stops:      true,
				StopGroups: true,
			},
		},
	}
}
