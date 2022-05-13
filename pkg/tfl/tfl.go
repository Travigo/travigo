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
	// file, err := os.Open("../test-data/tfl-route-short.json")
	if err != nil {
		return err
	}
	defer file.Body.Close()
	// defer file.Close()

	routes := RouteAPI{}
	err = routes.ParseJSON(file.Body, *datasource)
	// err = routes.ParseJSON(file, *datasource)
	if err != nil {
		return err
	}
	routes.ImportIntoMongoAsCTDF()

	for _, line := range routes.Lines {
		err = line.GetSequence()

		if err != nil {
			return err
		}

		err = line.GenerateCTDFJourneys(*datasource)
	}

	return nil
}
