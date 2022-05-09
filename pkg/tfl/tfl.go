package tfl

import (
	"net/http"
	"time"

	"github.com/britbus/britbus/pkg/ctdf"
)

func ImportTFLData() error {
	datasource := &ctdf.DataSource{
		OriginalFormat: "tfl-api",
		Provider:       "TransportForLondon",
		Identifier:     time.Now().Format(time.RFC3339),
	}

	// Do CTDF Services
	file, err := http.Get("https://api.tfl.gov.uk/Line/Mode/bus/Route?serviceTypes=regular,night")
	if err != nil {
		return err
	}
	defer file.Body.Close()

	routes := RouteAPI{}
	err = routes.ParseJSON(file.Body, *datasource)
	if err != nil {
		return err
	}
	routes.ImportIntoMongoAsCTDF()

	return nil
}
