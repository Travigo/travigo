package manager

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
			SupportedObjects: SupportedObjects{
				Operators: true,
			},
		},
	}
}
