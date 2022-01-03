package transxchange

import (
	"encoding/xml"
	"io"
	"log"
	"os"
)

func ParseXMLFile(file string) (*TransXChange, error) {
	transXChange := TransXChange{}
	// naptan.StopPoints = []StopPoint{}

	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	d := xml.NewDecoder(f)
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatalf("Error decoding token: %s", err)
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
			} else if ty.Name.Local == "Operator" {
				var operator Operator

				if err = d.DecodeElement(&operator, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					transXChange.Operators = append(transXChange.Operators, operator)
				}
			} else if ty.Name.Local == "Route" {
				var route Route

				if err = d.DecodeElement(&route, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					transXChange.Routes = append(transXChange.Routes, route)
				}
			} else if ty.Name.Local == "Service" {
				var service Service

				if err = d.DecodeElement(&service, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					transXChange.Services = append(transXChange.Services, service)
				}
			} else if ty.Name.Local == "JourneyPatternSection" {
				var jps JourneyPatternSection

				if err = d.DecodeElement(&jps, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					transXChange.JourneyPatternSections = append(transXChange.JourneyPatternSections, jps)
				}
			} else if ty.Name.Local == "RouteSection" {
				var routeSection RouteSection

				if err = d.DecodeElement(&routeSection, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					transXChange.RouteSections = append(transXChange.RouteSections, routeSection)
				}
			} else if ty.Name.Local == "VehicleJourney" {
				var vehicleJourney VehicleJourney

				if err = d.DecodeElement(&vehicleJourney, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					vehicleJourney.OperatingProfile.ParseXMLValue()
					transXChange.VehicleJourneys = append(transXChange.VehicleJourneys, vehicleJourney)
				}
				// vehicleJourney := parseVehicleJourney(&tok)
				// transXChange.VehicleJourneys = append(transXChange.VehicleJourneys, vehicleJourney)
			}
		default:
		}
	}

	return &transXChange, nil
}

func parseVehicleJourney(element *xml.Token) VehicleJourney {
	vehicleJourney := VehicleJourney{}

	return vehicleJourney
}