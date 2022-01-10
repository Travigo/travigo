package transxchange

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/rs/zerolog/log"
)

type OperatingProfile struct {
	XMLValue string `xml:",innerxml" json:"-" bson:"-"`

	RegularDayType              []string
	PeriodicDayType             []string
	BankHolidayOperation        []string
	BankHolidayNonOperation     []string
	ServicedOrganisationDayType []string
	SpecialDaysOperation        []string
}

// This is a bit hacky and doesn't seem like the best way of doing it but it works
func (operatingProfile *OperatingProfile) ToCTDF() (*ctdf.Availability, error) {
	ctdfAvailability := ctdf.Availability{}

	operatingProfile.RegularDayType = []string{}
	operatingProfile.BankHolidayOperation = []string{}
	operatingProfile.BankHolidayNonOperation = []string{}

	// var field string
	elementChain := []string{}

	d := xml.NewDecoder(strings.NewReader(operatingProfile.XMLValue))
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			return nil, err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			elementChain = append(elementChain, ty.Name.Local)

			switch elementChain[0] {
			case "RegularDayType":
				if len(elementChain) == 1 {
					break
				}

				if elementChain[1] == "DaysOfWeek" && len(elementChain) == 3 {
					ctdfAvailability.Match = append(ctdfAvailability.Match, ctdf.AvailabilityRecord{
						Type:  ctdf.AvailabilityDayOfWeek,
						Value: elementChain[2],
					})
				}
			case "BankHolidayOperation":
				if len(elementChain) == 1 {
					break
				}

				if (elementChain[1] == "DaysOfNonOperation" || elementChain[1] == "DaysOfOperation") && len(elementChain) == 3 {
					var record ctdf.AvailabilityRecord
					if elementChain[2] == "OtherPublicHoliday" {
						var otherPublicHoliday struct {
							Description string
							Date        string
						}
						if err = d.DecodeElement(&otherPublicHoliday, &ty); err != nil {
							log.Fatal().Msgf("Error decoding item: %s", err)
						}
						record = ctdf.AvailabilityRecord{
							Type:        ctdf.AvailabilityDate,
							Value:       otherPublicHoliday.Date,
							Description: otherPublicHoliday.Description,
						}
					} else {
						record = ctdf.AvailabilityRecord{
							Type:  ctdf.AvailabilitySpecialDay,
							Value: fmt.Sprintf("GB:BankHoliday:%s", elementChain[2]),
						}
					}

					if elementChain[1] == "DaysOfOperation" {
						ctdfAvailability.Match = append(ctdfAvailability.Match, record)
					} else if elementChain[1] == "DaysOfNonOperation" {
						ctdfAvailability.Exclude = append(ctdfAvailability.Exclude, record)
					}
				}
			case "SpecialDaysOperation", "PeriodicDayType", "ServicedOrganisationDayType":
				log.Error().Msgf("Cannot parse OperatingProfile type %s", elementChain[0])
			}
		case xml.EndElement:
			elementChain = elementChain[:len(elementChain)-1]
		}
	}

	return &ctdfAvailability, nil
}
