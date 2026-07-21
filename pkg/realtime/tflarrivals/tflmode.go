package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/journeytracks"
)

type TfLMode struct {
	ModeID        string
	TransportType ctdf.TransportType

	Lines map[string]*TfLLine

	TrackArrivals    bool
	TrackDisruptions bool

	ArrivalRefreshRate    time.Duration
	DisruptionRefreshRate time.Duration
}

func (m *TfLMode) LoadImportedRouteTracks() error {
	for _, line := range m.Lines {
		line.OrderedLineRoutes = nil
	}

	routes, err := journeytracks.Load(context.Background(), journeytracks.Filter{
		DatasetID:  "gb-tfl-route-tracks",
		Attributes: map[string]string{"mode": m.ModeID},
	})
	if err != nil {
		return err
	}
	for _, importedRoute := range routes {
		metadata := importedRoute.Metadata
		lineID := metadata.Attributes["line"]
		if m.Lines[lineID] == nil {
			continue
		}
		route := OrderedLineRoute{Name: metadata.RouteName, NaptanIDs: metadata.ExternalStopRefs, Direction: metadata.Direction}
		route.LegTracks = make([][]ctdf.Location, len(metadata.ExternalStopRefs)-1)
		route.LegTrackRefs = make([]string, len(metadata.ExternalStopRefs)-1)
		for legIndex, journeyTrack := range importedRoute.Legs {
			if legIndex >= 0 && legIndex < len(route.LegTracks) {
				route.LegTracks[legIndex] = journeyTrack.Track
				route.LegTrackRefs[legIndex] = journeyTrack.PrimaryIdentifier
			}
		}
		m.Lines[lineID].OrderedLineRoutes = append(m.Lines[lineID].OrderedLineRoutes, route)
	}
	for _, line := range m.Lines {
		line.StopOccurrenceLimits = tflRouteStopOccurrenceLimits(line.OrderedLineRoutes)
	}
	return nil
}

func (m *TfLMode) GetLines() {
	m.Lines = map[string]*TfLLine{}

	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/Line/Mode/%s?app_key=%s", m.ModeID, TfLAppKey)
	req, _ := http.NewRequest("GET", requestURL, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	jsonBytes, _ := io.ReadAll(resp.Body)

	var modeLines []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	json.Unmarshal(jsonBytes, &modeLines)

	for _, line := range modeLines {
		tflLine := &TfLLine{
			ModeID:        m.ModeID,
			LineID:        line.ID,
			LineName:      line.Name,
			TransportType: m.TransportType,
		}
		tflLine.GetService()

		m.Lines[line.ID] = tflLine
	}
}
