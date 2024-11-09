package naptan

import (
	"fmt"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
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

	StopClassification StopClassification

	StopAreas []StopPointStopAreaRef `xml:"StopAreas>StopAreaRef"`
}

type StopClassification struct {
	StopType string

	OnStreet struct {
		Bus *BusStopClassification
	}

	OffStreet struct {
		Bus   *BusStopClassification
		Rail  *RailStopClassification
		Coach *CoachStopClassification
		Metro *MetroStopClassification
	}
}

type BusStopClassification struct {
	BusStopType string
	Bearing     string `xml:"MarkedPoint>Bearing>CompassPoint"`
}

type RailStopClassification struct {
	AnnotatedRailRef struct {
		TiplocRef   string
		CrsRef      string
		StationName string
	}
}

type CoachStopClassification struct {
}

type MetroStopClassification struct {
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

	switch orig.StopClassification.StopType {
	case "BCT", "BCS", "BCQ": // busCoachTramStopOnStreet, busCoachTramStationBay, busCoachTramStationVariableBay
		if orig.StopClassification.OnStreet.Bus != nil || orig.StopClassification.OffStreet.Bus != nil {
			TransportTypes = append(TransportTypes, ctdf.TransportTypeBus)
		}

		if orig.StopClassification.OffStreet.Coach != nil {
			TransportTypes = append(TransportTypes, ctdf.TransportTypeCoach)
		}

		if orig.StopClassification.OffStreet.Metro != nil {
			TransportTypes = append(TransportTypes, ctdf.TransportTypeMetro)
		}
	case "BST", "BCE", "BCP": // busCoachAccess, busCoachStationEntrance, busCoachPrivate
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeBus,
			ctdf.TransportTypeCoach,
		}
	case "RPLY": // railPlatform
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeRail,
		}
	case "RLY": // railAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeRail,
		}
	case "RSE": // railStationEntrance
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeRail,
		}
	case "PLT": // tramMetroOrUndergroundPlatform
		if orig.StopClassification.OffStreet.Rail != nil {
			TransportTypes = append(TransportTypes, ctdf.TransportTypeRail)
		} else {
			TransportTypes = []ctdf.TransportType{
				ctdf.TransportTypeTram,
				ctdf.TransportTypeMetro,
			}
		}
	case "MET": // tramMetroOrUndergroundAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeRail,
			ctdf.TransportTypeMetro,
		}
	case "TMU": // tramMetroOrUndergroundEntrance
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeRail,
			ctdf.TransportTypeMetro,
		}
	case "FER": // ferryOrPortAccess
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeFerry,
		}
	case "FTD": // ferryTerminalDockEntrance
		TransportTypes = []ctdf.TransportType{
			ctdf.TransportTypeFerry,
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

	var descriptor []string

	if orig.Descriptor.Indicator != "" && orig.Descriptor.Indicator != "--" {
		descriptor = append(descriptor, orig.Descriptor.Indicator)
	}

	if orig.Descriptor.Landmark != "" && orig.Descriptor.Landmark != "--" {
		descriptor = append(descriptor, orig.Descriptor.Landmark)
	}

	primaryIdentifier := fmt.Sprintf(ctdf.GBStopIDFormat, orig.AtcoCode)
	ctdfStop := ctdf.Stop{
		PrimaryIdentifier: primaryIdentifier,
		OtherIdentifiers:  []string{primaryIdentifier},
		PrimaryName:       orig.Descriptor.CommonName,
		Descriptor:        strings.Join(descriptor, " "),

		CreationDateTime:     creationTime,
		ModificationDateTime: modificationTime,
		TransportTypes:       TransportTypes,
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{orig.Location.Longitude, orig.Location.Latitude},
		},

		Active:   orig.Status == "active",
		Timezone: "Europe/London",
	}

	if orig.AtcoCode != "" {
		ctdfStop.OtherIdentifiers = append(ctdfStop.OtherIdentifiers, fmt.Sprintf("gb-atco-%s", orig.AtcoCode))
	}
	if orig.NaptanCode != "" {
		ctdfStop.OtherIdentifiers = append(ctdfStop.OtherIdentifiers, fmt.Sprintf("gb-naptan-%s", orig.NaptanCode))
	}
	if orig.StopClassification.OffStreet.Rail != nil && orig.StopClassification.OffStreet.Rail.AnnotatedRailRef.TiplocRef != "" {
		ctdfStop.OtherIdentifiers = append(ctdfStop.OtherIdentifiers, fmt.Sprintf("gb-tiploc-%s", orig.StopClassification.OffStreet.Rail.AnnotatedRailRef.TiplocRef))
	}
	if orig.StopClassification.OffStreet.Rail != nil && orig.StopClassification.OffStreet.Rail.AnnotatedRailRef.CrsRef != "" {
		ctdfStop.OtherIdentifiers = append(ctdfStop.OtherIdentifiers, fmt.Sprintf("gb-crs-%s", orig.StopClassification.OffStreet.Rail.AnnotatedRailRef.CrsRef))
	}

	for i := 0; i < len(orig.StopAreas); i++ {
		stopArea := orig.StopAreas[i]

		ctdfStop.Associations = append(ctdfStop.Associations, &ctdf.Association{
			Type:                 "stop_group",
			AssociatedIdentifier: fmt.Sprintf(ctdf.StopGroupIDFormat, stopArea.StopAreaCode),
		})
	}

	return &ctdfStop
}
