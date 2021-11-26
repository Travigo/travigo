package main

import (
	"encoding/xml"
	"io"
	"log"
	"os"
)

// StopPoint related structs
type StopPoint struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	AtcoCode              string
	NaptanCode            string
	AdministrativeAreaRef string

	Descriptor         *StopPointDescriptor
	Place              *StopPointPlace
	StopClassification *StopPointStopClassification

	StopAreas []StopPointStopAreaRef `xml:"StopAreas>StopAreaRef"`
}

type StopPointDescriptor struct {
	CommonName      string
	ShortCommonName string
	Landmark        string
	Street          string
	Indicator       string
}

type StopPointPlace struct {
	NptgLocalityRef string
	LocalityCentre  bool
	Location        *Location
}

type StopPointStopAreaRef struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode string `xml:",chardata"`
}

type StopPointStopClassification struct {
	StopType       string
	BusStopType    string `xml:"OnStreet>Bus>BusStopType"`
	BusStopBearing string `xml:"OnStreet>Bus>MarkedPoint>Bearing>CompassPoint"`
}

// StopArea related structs
type StopArea struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`
	Status               string `xml:",attr"`

	StopAreaCode          string
	Name                  string
	AdministrativeAreaRef string
	StopAreaType          string

	Location *Location
}

// Shared
type Location struct {
	GridType  string
	Easting   string
	Northing  string
	Longitude float64
	Latitude  float64
}

func main() {
	f, err := os.Open(os.Args[1])
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
		}

		switch ty := tok.(type) {
		case xml.StartElement:
			if ty.Name.Local == "StopPoint" {
				log.Println("Found StopPoint")
				var stopPoint StopPoint
				if err = d.DecodeElement(&stopPoint, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				}
			} else if ty.Name.Local == "StopArea" {
				log.Println("Found StopArea")
				var stopArea StopArea
				if err = d.DecodeElement(&stopArea, &ty); err != nil {
					log.Fatalf("Error decoding item: %s", err)
				}

				log.Println(stopArea)
				log.Println(stopArea.Location)
			}
		default:
		}
	}
}
