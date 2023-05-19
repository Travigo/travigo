package travelinenoc

import (
	"encoding/xml"
	"io"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/util"
	"golang.org/x/net/html/charset"
)

func ParseXMLFile(reader io.Reader) (*TravelineData, error) {
	travelineData := TravelineData{}

	d := xml.NewDecoder(util.NewValidUTF8Reader(reader))
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
			if ty.Name.Local == "travelinedata" {
				for i := 0; i < len(ty.Attr); i++ {
					attr := ty.Attr[i]

					switch attr.Name.Local {
					case "generationDate":
						travelineData.GenerationDate = attr.Value
					}
				}
			} else if ty.Name.Local == "NOCLinesRecord" {
				var NOCLinesRecord NOCLinesRecord

				if err = d.DecodeElement(&NOCLinesRecord, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					travelineData.NOCLinesRecords = append(travelineData.NOCLinesRecords, NOCLinesRecord)
				}
			} else if ty.Name.Local == "NOCTableRecord" {
				var NOCTableRecord NOCTableRecord

				if err = d.DecodeElement(&NOCTableRecord, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					travelineData.NOCTableRecords = append(travelineData.NOCTableRecords, NOCTableRecord)
				}
			} else if ty.Name.Local == "OperatorsRecord" {
				var operatorRecord OperatorsRecord

				if err = d.DecodeElement(&operatorRecord, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					travelineData.OperatorsRecords = append(travelineData.OperatorsRecords, operatorRecord)
				}
			} else if ty.Name.Local == "GroupsRecord" {
				var groupRecord GroupsRecord

				if err = d.DecodeElement(&groupRecord, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					travelineData.GroupsRecords = append(travelineData.GroupsRecords, groupRecord)
				}
			} else if ty.Name.Local == "ManagementDivisionsRecord" {
				var managementRecord ManagementDivisionsRecord

				if err = d.DecodeElement(&managementRecord, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					travelineData.ManagementDivisionsRecords = append(travelineData.ManagementDivisionsRecords, managementRecord)
				}
			} else if ty.Name.Local == "PublicNameRecord" {
				var publicNameRecord PublicNameRecord

				if err = d.DecodeElement(&publicNameRecord, &ty); err != nil {
					log.Fatal().Msgf("Error decoding item: %s", err)
				} else {
					travelineData.PublicNameRecords = append(travelineData.PublicNameRecords, publicNameRecord)
				}
			}
		default:
		}
	}

	log.Info().Msgf("Successfully parsed document")
	log.Info().Msgf(" - Contains %d NOCLinesRecords", len(travelineData.NOCLinesRecords))
	log.Info().Msgf(" - Contains %d NOCTableRecords", len(travelineData.NOCTableRecords))
	log.Info().Msgf(" - Contains %d OperatorRecords", len(travelineData.OperatorsRecords))
	log.Info().Msgf(" - Contains %d GroupsRecords", len(travelineData.GroupsRecords))
	log.Info().Msgf(" - Contains %d ManagementDivisionsRecords", len(travelineData.ManagementDivisionsRecords))
	log.Info().Msgf(" - Contains %d PublicNameRecords", len(travelineData.PublicNameRecords))

	return &travelineData, nil
}
