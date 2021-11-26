package naptan

import (
	"fmt"

	"github.com/paulcager/osgridref"
)

type Location struct {
	GridType  string
	Easting   string
	Northing  string
	Longitude string
	Latitude  string
}

func (l *Location) ConvertOSGridRef() {
	// Only bother converting the OSGridRef if lat/long isnt set and easting/northing is set
	if l.Easting != "" && l.Northing != "" && (l.Latitude == "" || l.Longitude == "") {
		gridRef, err := osgridref.ParseOsGridRef(fmt.Sprintf("%s,%s", l.Easting, l.Northing))
		if err != nil {
			panic(err)
		}

		lat, lon := gridRef.ToLatLon()

		l.Latitude = fmt.Sprintf("%.4f", lat)
		l.Longitude = fmt.Sprintf("%.4f", lon)
	}
}
