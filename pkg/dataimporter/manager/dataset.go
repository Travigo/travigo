package manager

type DataSet struct {
	Identifier string
	Format     DataSetFormat

	Provider Provider

	Source string

	UnpackBundle BundleFormat

	SupportedObjects SupportedObjects
}

type DataSetFormat string

const (
	DataSetFormatNaPTAN            DataSetFormat = "gb-naptan"
	DataSetFormatTransXChange                    = "gb-transxchange"
	DataSetFormatTravelineNOC                    = "gb-travelinenov"
	DataSetFormatCIF                             = "gb-cif"
	DataSetFormatNationalRailTOC                 = "gb-nationalrailtoc"
	DataSetFormatNetworkRailCorpus               = "gb-networkrailcorpus"
	DataSetFormatSiriVM                          = "eu-siri-vm"
	DataSetFormatGTFSSchedule                    = "gtfs-schedule"
	DataSetFormatGTFSRealtime                    = "gtfs-realtime"
)

type Provider struct {
	Name    string
	Website string
}

type BundleFormat string

const (
	BundleFormatNone  BundleFormat = "none"
	BundleFormatZIP                = "zip"
	BundleFormatTarGZ              = "tar.gz"
)

type SupportedObjects struct {
	Operators bool
	Stops     bool
	Services  bool
	Journeys  bool

	RealtimeJourneys bool
	ServiceAlerts    bool
}
