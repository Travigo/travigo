package transxchange

import (
	"encoding/xml"
	"io"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/html/charset"
)

func ParseXMLFile(reader io.Reader) (*TransXChange, error) {
	transXChange := TransXChange{}

	d := xml.NewDecoder(reader)
	d.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatal().Msgf("Error decoding token: %s", err)
			return nil, err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			if ty.Name.Local == "TransXChange" {
				for i := 0; i < len(ty.Attr); i++ {
					attr := ty.Attr[i]

					switch attr.Name.Local {
					case "CreationDateTime":
						transXChange.CreationDateTime = attr.Value
					case "ModificationDateTime":
						transXChange.ModificationDateTime = attr.Value
					case "SchemaVersion":
						transXChange.SchemaVersion = attr.Value
					}
				}

				validate := transXChange.Validate()
				if validate != nil {
					return nil, validate
				}
			} else if ty.Name.Local == "StopPoint" {
				var stopPoint StopPoint

				if err = d.DecodeElement(&stopPoint, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.StopPoints = append(transXChange.StopPoints, &stopPoint)
				}
			} else if ty.Name.Local == "Operator" || ty.Name.Local == "LicensedOperator" {
				var operator Operator

				if err = d.DecodeElement(&operator, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.Operators = append(transXChange.Operators, &operator)
				}
			} else if ty.Name.Local == "Route" {
				var route Route

				if err = d.DecodeElement(&route, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.Routes = append(transXChange.Routes, &route)
				}
			} else if ty.Name.Local == "Service" {
				var service Service

				if err = d.DecodeElement(&service, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.Services = append(transXChange.Services, &service)
				}
			} else if ty.Name.Local == "JourneyPatternSection" {
				var jps JourneyPatternSection

				if err = d.DecodeElement(&jps, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.JourneyPatternSections = append(transXChange.JourneyPatternSections, &jps)
				}
			} else if ty.Name.Local == "RouteSection" {
				var routeSection RouteSection

				if err = d.DecodeElement(&routeSection, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.RouteSections = append(transXChange.RouteSections, &routeSection)
				}
			} else if ty.Name.Local == "VehicleJourney" {
				var vehicleJourney VehicleJourney

				if err = d.DecodeElement(&vehicleJourney, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.VehicleJourneys = append(transXChange.VehicleJourneys, &vehicleJourney)
				}
			} else if ty.Name.Local == "ServicedOrganisation" {
				var org ServicedOrganisation

				if err = d.DecodeElement(&org, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					transXChange.ServicedOrganisations = append(transXChange.ServicedOrganisations, &org)
				}
			}
		default:
		}
	}

	log.Debug().Msgf("Successfully parsed document")
	log.Debug().Msgf(" - Last modified %s", transXChange.ModificationDateTime)
	log.Debug().Msgf(" - Contains %d operators", len(transXChange.Operators))
	log.Debug().Msgf(" - Contains %d services", len(transXChange.Services))
	log.Debug().Msgf(" - Contains %d routes", len(transXChange.Routes))
	log.Debug().Msgf(" - Contains %d route sections", len(transXChange.RouteSections))
	log.Debug().Msgf(" - Contains %d vehicle journeys", len(transXChange.VehicleJourneys))

	return &transXChange, nil
}

func parseVehicleJourney(element *xml.Token) VehicleJourney {
	vehicleJourney := VehicleJourney{}

	return vehicleJourney
}
