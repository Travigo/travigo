package datasets

import (
	"time"

	"github.com/adjust/rmq/v5"
)

type DataSet struct {
	Identifier    string        `groups:"web-datasource"`
	DataSourceRef string        `json:"-"`
	Format        DataSetFormat `groups:"web-datasource"`

	Provider Provider `groups:"web-datasource"`

	Source               string                `groups:"web-datasource"`
	SourceAuthentication *SourceAuthentication `json:"-"`

	DatasetSize     string
	RefreshInterval time.Duration

	UnpackBundle      BundleFormat     `json:"-"`
	SupportedObjects  SupportedObjects `groups:"web-datasource"`
	IgnoreObjects     IgnoreObjects
	ImportDestination ImportDestination `json:"-"`

	CustomConfig map[string]string

	LinkedDataset string

	// Internal only
	Queue *rmq.Queue `json:"-"`
}

type SourceAuthentication struct {
	Query      map[string]string
	Header     map[string]string
	AuthHeader string
	Basic      struct {
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
	DataSetFormatTfLRouteTracks                  = "gb-tfl-route-tracks"
	DataSetFormatOSMRailTracks                   = "gb-osm-rail-tracks"
)

type Provider struct {
	Name    string `groups:"web-datasource"`
	Website string `groups:"web-datasource"`
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
	ImportDestinationDatabase       ImportDestination = "database"
	ImportDestinationRealtimeQueue  ImportDestination = "realtime-queue"
	ImportDestinationSpecificRunner ImportDestination = "specific-runner"
)
