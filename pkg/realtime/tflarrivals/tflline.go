package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type TfLLine struct {
	LineID        string
	LineName      string
	TransportType ctdf.TransportType

	OrderedLineRoutes []OrderedLineRoute

	Service *ctdf.Service
}

func (l *TfLLine) GetService() {
	collection := database.GetCollection("services")

	query := bson.M{
		"operatorref":   "gb-noc-TFLO",
		"servicename":   l.LineName,
		"transporttype": l.TransportType,
	}

	collection.FindOne(context.Background(), query).Decode(&l.Service)
}

func (l *TfLLine) GetTfLRouteSequences() {
	for _, direction := range []string{"inbound", "outbound"} {
		requestURL := fmt.Sprintf(
			"https://api.tfl.gov.uk/line/%s/route/sequence/%s/?app_key=%s",
			l.LineID,
			direction,
			TfLAppKey)
		req, _ := http.NewRequest("GET", requestURL, nil)
		req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

		client := &http.Client{}
		resp, err := client.Do(req)

		if err != nil {
			log.Fatal().Err(err).Msg("Download file")
		}
		defer resp.Body.Close()

		jsonBytes, _ := io.ReadAll(resp.Body)

		var routeSequenceResponse RouteSequenceResponse
		json.Unmarshal(jsonBytes, &routeSequenceResponse)

		l.OrderedLineRoutes = append(l.OrderedLineRoutes, routeSequenceResponse.OrderedLineRoutes...)
	}
}
