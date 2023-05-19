package naptan

import (
	"fmt"

	"github.com/paulcager/osgridref"
)

type Location struct {
	GridType  string
	Easting   string
	Northing  string
	Longitude float64 `xml:"Translation>Longitude"`
	Latitude  float64 `xml:"Translation>Latitude"`
}

func (l *Location) UpdateCoordinates() {
	// Only bother converting the OSGridRef if lat/lon isnt set and easting/northing is set
	if l.GridType == "UKOS" && l.Easting != "" && l.Northing != "" && (l.Latitude == 0 || l.Longitude == 0) {
		gridRef, err := osgridref.ParseOsGridRef(fmt.Sprintf("%s,%s", l.Easting, l.Northing))
		if err != nil {
			panic(err)
		}

		lat, lon := gridRef.ToLatLon()

		l.Latitude = lat
		l.Longitude = lon
	}
}
