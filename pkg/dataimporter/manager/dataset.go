package manager

import (
	"net/http"

	"github.com/travigo/travigo/pkg/dataimporter/formats"
)

type DataSet struct {
	Identifier string
	Format     DataSetFormat

	Provider Provider

	Source string

	UnpackBundle BundleFormat

	SupportedObjects formats.SupportedObjects

	DownloadHandler func(*http.Request)
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
	BundleFormatGZ                 = "gz"
	BundleFormatTarGZ              = "tar.gz"
)
