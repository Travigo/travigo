package naptan

import (
	"encoding/xml"
	"io"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/html/charset"
)

func (n *NaPTAN) ParseFile(reader io.Reader) error {
	n.StopPoints = []*StopPoint{}
	n.StopAreas = []*StopArea{}

	d := xml.NewDecoder(reader)
	d.CharsetReader = charset.NewReaderLabel
	for {
		tok, err := d.Token()
		if tok == nil || err == io.EOF {
			// EOF means we're done.
			break
		} else if err != nil {
			log.Fatal().Msgf("Error decoding token: %s", err)
			return err
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			if ty.Name.Local == "NaPTAN" {
				for i := 0; i < len(ty.Attr); i++ {
					attr := ty.Attr[i]

					switch attr.Name.Local {
					case "CreationDateTime":
						n.CreationDateTime = attr.Value
					case "ModificationDateTime":
						n.ModificationDateTime = attr.Value
					case "SchemaVersion":
						n.SchemaVersion = attr.Value
					}
				}

				validate := n.Validate()
				if validate != nil {
					return validate
				}
			} else if ty.Name.Local == "StopPoint" {
				var stopPoint StopPoint

				if err = d.DecodeElement(&stopPoint, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					stopPoint.Location.UpdateCoordinates()
					n.StopPoints = append(n.StopPoints, &stopPoint)
				}
			} else if ty.Name.Local == "StopArea" {
				var stopArea StopArea

				if err = d.DecodeElement(&stopArea, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					stopArea.Location.UpdateCoordinates()
					n.StopAreas = append(n.StopAreas, &stopArea)
				}
			}
		default:
		}
	}

	log.Info().Msgf("Successfully parsed document")
	log.Info().Msgf(" - Last modified %s", n.ModificationDateTime)
	log.Info().Msgf(" - Contains %d stops", len(n.StopPoints))
	log.Info().Msgf(" - Contains %d stop areas", len(n.StopAreas))

	return nil
}
