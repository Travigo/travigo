package transxchange

import (
	"encoding/xml"
	"io"
	"log"
	"strings"
)

type VehicleJourney struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	SequenceNumber       string `xml:",attr"`

	PrivateCode        string
	OperatorRef        string
	Direction          string
	GarageRef          string
	VehicleJourneyCode string
	ServiceRef         string
	LineRef            string
	JourneyPatternRef  string
	DepartureTime      string

	VehicleJourneyTimingLinks []VehicleJourneyTimingLink `xml:"VehicleJourneyTimingLink"`

	OperatingProfile OperatingProfile // `xml:",innerxml" json:"-" bson:"-"`
}

func (v *VehicleJourney) GetVehicleJourneyTimingLinkByJourneyPatternTimingLinkRef(ID string) *VehicleJourneyTimingLink {
	for _, vehicleJourneyTimingLink := range v.VehicleJourneyTimingLinks {
		if vehicleJourneyTimingLink.JourneyPatternTimingLinkRef == ID {
			return &vehicleJourneyTimingLink
		}
	}

	return nil
}

type VehicleJourneyTimingLink struct {
	ID string `xml:"id,attr"`

	JourneyPatternTimingLinkRef string
	RunTime                     string
}

type OperatingProfile struct {
	XMLValue string `xml:",innerxml" json:"-" bson:"-"`

	RegularDayType          []string
	BankHolidayOperation    []string
	BankHolidayNonOperation []string
}

// This is a bit hacky and doesn't seem like the best way of doing it but it works
func (operatingProfile *OperatingProfile) ParseXMLValue() {
	operatingProfile.RegularDayType = []string{}
	operatingProfile.BankHolidayOperation = []string{}
	operatingProfile.BankHolidayNonOperation = []string{}

	var field string

	d := xml.NewDecoder(strings.NewReader(operatingProfile.XMLValue))
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatalf("Error decoding token: %s", err)
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			switch ty.Name.Local {
			case "DaysOfWeek":
			case "RegularDayType", "DaysOfOperation", "DaysOfNonOperation", "BankHolidayOperation":
				field = ty.Name.Local
			default:
				if field == "RegularDayType" {
					operatingProfile.RegularDayType = append(operatingProfile.RegularDayType, ty.Name.Local)
				} else if field == "DaysOfOperation" {
					operatingProfile.BankHolidayOperation = append(operatingProfile.BankHolidayOperation, ty.Name.Local)
				} else if field == "DaysOfNonOperation" {
					operatingProfile.BankHolidayNonOperation = append(operatingProfile.BankHolidayNonOperation, ty.Name.Local)
				}
			}
		}
	}
}
