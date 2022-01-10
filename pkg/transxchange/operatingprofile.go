package transxchange

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/britbus/britbus/pkg/ctdf"
	"github.com/kr/pretty"
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
			pretty.Println(elementChain)
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

						elementChain = elementChain[:len(elementChain)-1] // Using decodeElement means we skip the end element for this
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
			case "SpecialDaysOperation":
				if len(elementChain) == 1 {
					type DateRange struct {
						StartDate string
						EndDate   string
						Note      string
					}
					type SpecialDaysOperation struct {
						DaysOfOperation    []DateRange `xml:"DaysOfOperation>DateRange"`
						DaysOfNonOperation []DateRange `xml:"DaysOfNonOperation>DateRange"`
					}

					var specialDaysOperation SpecialDaysOperation

					if err = d.DecodeElement(&specialDaysOperation, &ty); err != nil {
						log.Fatal().Msgf("Error decoding item: %s", err)
					}

					for _, dayOfOperation := range specialDaysOperation.DaysOfOperation {
						ctdfAvailability.Match = append(ctdfAvailability.Match, ctdf.AvailabilityRecord{
							Type:        ctdf.AvailabilityDateRange,
							Value:       fmt.Sprintf("%s:%s", dayOfOperation.StartDate, dayOfOperation.EndDate),
							Description: dayOfOperation.Note,
						})
					}
					for _, dayOfNonOperation := range specialDaysOperation.DaysOfNonOperation {
						ctdfAvailability.Exclude = append(ctdfAvailability.Exclude, ctdf.AvailabilityRecord{
							Type:        ctdf.AvailabilityDateRange,
							Value:       fmt.Sprintf("%s:%s", dayOfNonOperation.StartDate, dayOfNonOperation.EndDate),
							Description: dayOfNonOperation.Note,
						})
					}

					elementChain = elementChain[:len(elementChain)-1] // Using decodeElement means we skip the end element for this
				}
			case "PeriodicDayType":
				return nil, errors.New(fmt.Sprintf("WIP OperatingProfile record type %s", elementChain[0]))
			case "ServicedOrganisationDayType":
				return nil, errors.New(fmt.Sprintf("OperatingProfile record type %s cannot be parsed", elementChain[0]))
			default:
				return nil, errors.New(fmt.Sprintf("Cannot parse OperatingProfile record type %s", elementChain[0]))
			}
		case xml.EndElement:
			elementChain = elementChain[:len(elementChain)-1]
		}
	}

	return &ctdfAvailability, nil
}
