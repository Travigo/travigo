package tflarrivals

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
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
			LineID:        line.ID,
			LineName:      line.Name,
			TransportType: m.TransportType,
		}
		tflLine.GetService()

		m.Lines[line.ID] = tflLine
	}
}
