package naptan

import (
	"fmt"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
)

type StopPoint struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	AtcoCode              string
	NaptanCode            string
	AdministrativeAreaRef string

	Descriptor *StopPointDescriptor

	NptgLocalityRef string    `xml:"Place>NptgLocalityRef"`
	LocalityCentre  bool      `xml:"Place>LocalityCentre"`
	Location        *Location `xml:"Place>Location"`

	StopType       string `xml:"StopClassification>StopType"`
	BusStopType    string `xml:"StopClassification>OnStreet>Bus>BusStopType"`
	BusStopBearing string `xml:"StopClassification>OnStreet>Bus>MarkedPoint>Bearing>CompassPoint"`

	StopAreas []StopPointStopAreaRef `xml:"StopAreas>StopAreaRef"`
}

type StopPointDescriptor struct {
	CommonName      string
	ShortCommonName string
	Landmark        string
	Street          string
	Indicator       string
}

type StopPointStopAreaRef struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode string `xml:",chardata"`
}

func (orig *StopPoint) ToCTDF() *ctdf.Stop {
	creationTime, _ := time.Parse(DateTimeFormat, orig.CreationDateTime)
	modificationTime, _ := time.Parse(DateTimeFormat, orig.ModificationDateTime)

	ctdfStop := ctdf.Stop{
		PrimaryIdentifier: fmt.Sprintf(ctdf.StopIDFormat, orig.AtcoCode),
		OtherIdentifiers: map[string]string{
			"AtcoCode":   orig.AtcoCode,
			"NaptanCode": orig.NaptanCode,
		},
		PrimaryName: orig.Descriptor.CommonName,
		OtherNames: map[string]string{
			"ShortCommonName": orig.Descriptor.ShortCommonName,
			"Street":          orig.Descriptor.Street,
			"Indicator":       orig.Descriptor.Indicator,
			"Landmark":        orig.Descriptor.Landmark,
		},

		CreationDateTime:     creationTime,
		ModificationDateTime: modificationTime,
		Status:               orig.Status,
		Type:                 "bus", //true for now
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{orig.Location.Longitude, orig.Location.Latitude},
		},
	}

	for i := 0; i < len(orig.StopAreas); i++ {
		stopArea := orig.StopAreas[i]

		ctdfStop.Associations = append(ctdfStop.Associations, ctdf.StopAssociation{
			Type:                 "stop_group",
			AssociatedIdentifier: fmt.Sprintf(ctdf.StopGroupIDFormat, stopArea.StopAreaCode),
		})
	}

	return &ctdfStop
}
