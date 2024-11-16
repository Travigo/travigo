package datasets

import (
	"net/http"

	"github.com/adjust/rmq/v5"
)

type DataSet struct {
	Identifier    string
	DataSourceRef string `json:"-"`
	Format        DataSetFormat

	Provider Provider

	Source               string
	SourceAuthentication SourceAuthentication `json:"-"`

	UnpackBundle      BundleFormat `json:"-"`
	SupportedObjects  SupportedObjects
	IgnoreObjects     IgnoreObjects
	ImportDestination ImportDestination `json:"-"`

	CustomConfig map[string]string

	LinkedDataset string

	DownloadHandler func(*http.Request) `json:"-"`

	// Internal only
	Queue *rmq.Queue `json:"-"`
}

type SourceAuthentication struct {
	Query  map[string]string
	Header map[string]string
	Basic  struct {
		Username string
		Password string
	}
	Custom string
}

type DataSetFormat string

const (
	DataSetFormatNaPTAN            DataSetFormat = "gb-naptan"
	DataSetFormatTransXChange                    = "gb-transxchange"
	DataSetFormatTravelineNOC                    = "gb-travelinenoc"
	DataSetFormatCIF                             = "gb-cif"
	DataSetFormatNationalRailTOC                 = "gb-nationalrailtoc"
	DataSetFormatNetworkRailCorpus               = "gb-networkrailcorpus"
	DataSetFormatSiriVM                          = "eu-siri-vm"
	DataSetFormatSiriSX                          = "eu-siri-sx"
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
	BundleFormatGZ                 = "gz"
	BundleFormatTarGZ              = "tar.gz"
)

type ImportDestination string

const (
	ImportDestinationDatabase      ImportDestination = "database"
	ImportDestinationRealtimeQueue                   = "realtime-queue"
)
