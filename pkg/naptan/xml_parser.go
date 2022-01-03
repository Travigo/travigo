package naptan

import (
	"encoding/xml"
	"io"
	"log"
	"os"
)

func ParseXMLFile(file string, matchesFilter func(interface{}) bool) (*NaPTAN, error) {
	naptan := NaPTAN{}
	naptan.StopPoints = []*StopPoint{}
	naptan.StopAreas = []*StopArea{}

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
			if ty.Name.Local == "NaPTAN" {
				for i := 0; i < len(ty.Attr); i++ {
					attr := ty.Attr[i]

					switch attr.Name.Local {
					case "CreationDateTime":
						naptan.CreationDateTime = attr.Value
					case "ModificationDateTime":
						naptan.ModificationDateTime = attr.Value
					case "SchemaVersion":
						naptan.SchemaVersion = attr.Value
					}
				}

				validate := naptan.Validate()
				if validate != nil {
					return nil, validate
				}
			} else if ty.Name.Local == "StopPoint" {
				var stopPoint StopPoint

				if err = d.DecodeElement(&stopPoint, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else if matchesFilter(&stopPoint) {
					stopPoint.Location.UpdateCoordinates()
					naptan.StopPoints = append(naptan.StopPoints, &stopPoint)
				}
			} else if ty.Name.Local == "StopArea" {
				var stopArea StopArea

				if err = d.DecodeElement(&stopArea, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else if matchesFilter(&stopArea) {
					stopArea.Location.UpdateCoordinates()
					naptan.StopAreas = append(naptan.StopAreas, &stopArea)
				}
			}
		default:
		}
	}

	return &naptan, nil
}
