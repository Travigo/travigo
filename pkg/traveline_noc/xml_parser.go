package travelinenoc

import (
	"encoding/xml"
	"io"
	"log"
	"os"

	"github.com/britbus/britbus/pkg/util"
)

func ParseXMLFile(file string) (*TravelineData, error) {
	travelineData := TravelineData{}

	f, err := os.Open(file)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()

	d := xml.NewDecoder(util.NewValidUTF8Reader(f))
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
			if ty.Name.Local == "NOCLinesRecord" {
				var NOCLinesRecord NOCLinesRecord

				if err = d.DecodeElement(&NOCLinesRecord, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					travelineData.NOCLinesRecords = append(travelineData.NOCLinesRecords, NOCLinesRecord)
				}
			} else if ty.Name.Local == "NOCTableRecord" {
				var NOCTableRecord NOCTableRecord

				if err = d.DecodeElement(&NOCTableRecord, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					travelineData.NOCTableRecords = append(travelineData.NOCTableRecords, NOCTableRecord)
				}
			} else if ty.Name.Local == "OperatorsRecord" {
				var operatorRecord OperatorsRecord

				if err = d.DecodeElement(&operatorRecord, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					travelineData.OperatorsRecords = append(travelineData.OperatorsRecords, operatorRecord)
				}
			} else if ty.Name.Local == "GroupsRecord" {
				var groupRecord GroupsRecord

				if err = d.DecodeElement(&groupRecord, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					travelineData.GroupsRecords = append(travelineData.GroupsRecords, groupRecord)
				}
			} else if ty.Name.Local == "ManagementDivisionsRecord" {
				var managementRecord ManagementDivisionsRecord

				if err = d.DecodeElement(&managementRecord, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					travelineData.ManagementDivisionsRecords = append(travelineData.ManagementDivisionsRecords, managementRecord)
				}
			} else if ty.Name.Local == "PublicNameRecord" {
				var publicNameRecord PublicNameRecord

				if err = d.DecodeElement(&publicNameRecord, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				} else {
					travelineData.PublicNameRecords = append(travelineData.PublicNameRecords, publicNameRecord)
				}
			}
		default:
		}
	}

	return &travelineData, nil
}
