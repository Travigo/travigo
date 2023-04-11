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

	RailTiplocRef string `xml:"StopClassification>OffStreet>Rail>AnnotatedRailRef>TiplocRef"`
	RailCrsRef    string `xml:"StopClassification>OffStreet>Rail>AnnotatedRailRef>CrsRef"`

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

	var TransportTypes []ctdf.TransportType

	switch orig.StopType {
	case "BCT": // busCoachTramStopOnStreet
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
			ctdf.TransportTypeTram,
		}
	case "BCS": // busCoachTramStationBay
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
			ctdf.TransportTypeTram,
		}
	case "BCQ": // busCoachTramStationVariableBay
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
			ctdf.TransportTypeTram,
		}
	case "BST": // busCoachAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
		}
	case "BCE": // busCoachStationEntrance
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
		}
	case "BCP": // busCoachPrivate
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
		}
	// case "RPLY": // railPlatform
	// 	TransportTypes = []ctdf.TransportType{
	// 		ctdf.TransportTypeTrain,
	// 	}
	case "RLY": // railAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeTrain,
		}
	// case "RSE": // railStationEntrance
	// 	TransportTypes = []ctdf.TransportType{
	// 		ctdf.TransportTypeTrain,
	// 	}
	case "PLT": // tramMetroOrUndergroundPlatform
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeTrain,
			ctdf.TransportTypeMetro,
		}
	case "MET": // tramMetroOrUndergroundAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeTrain,
			ctdf.TransportTypeMetro,
		}
	// case "TMU": // tramMetroOrUndergroundEntrance
	// 	TransportTypes = []ctdf.TransportType{
	// 		ctdf.TransportTypeTrain,
	// 		ctdf.TransportTypeMetro,
	// 	}
	case "FER": // ferryOrPortAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBoat,
		}
	case "FTD": // ferryTerminalDockEntrance
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBoat,
		}
	case "TXR": // taxiRank
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeTaxi,
		}
	case "STR": // sharedTaxiRank
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeTaxi,
		}
	case "AIR": // airportEntrance
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeAirport,
		}
	case "GAT": // airAccessArea
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeAirport,
		}
	default:
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeUnknown,
		}
	}

	ctdfStop := ctdf.Stop{
		PrimaryIdentifier: fmt.Sprintf(ctdf.StopIDFormat, orig.AtcoCode),
		OtherIdentifiers:  map[string]string{},
		PrimaryName:       orig.Descriptor.CommonName,
		OtherNames: map[string]string{
			"ShortCommonName": orig.Descriptor.ShortCommonName,
			"Street":          orig.Descriptor.Street,
			"Indicator":       orig.Descriptor.Indicator,
			"Landmark":        orig.Descriptor.Landmark,
		},

		CreationDateTime:     creationTime,
		ModificationDateTime: modificationTime,
		TransportTypes:       TransportTypes,
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{orig.Location.Longitude, orig.Location.Latitude},
		},

		Active: orig.Status == "active",
	}

	if orig.AtcoCode != "" {
		ctdfStop.OtherIdentifiers["AtcoCode"] = orig.AtcoCode
	}
	if orig.NaptanCode != "" {
		ctdfStop.OtherIdentifiers["NaptanCode"] = orig.NaptanCode
	}
	if orig.RailTiplocRef != "" {
		ctdfStop.OtherIdentifiers["Tiploc"] = orig.RailTiplocRef
	}
	if orig.RailCrsRef != "" {
		ctdfStop.OtherIdentifiers["Crs"] = orig.RailCrsRef
	}

	for i := 0; i < len(orig.StopAreas); i++ {
		stopArea := orig.StopAreas[i]

		ctdfStop.Associations = append(ctdfStop.Associations, &ctdf.StopAssociation{
			Type:                 "stop_group",
			AssociatedIdentifier: fmt.Sprintf(ctdf.StopGroupIDFormat, stopArea.StopAreaCode),
		})
	}

	return &ctdfStop
}
