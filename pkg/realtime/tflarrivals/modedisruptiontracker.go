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

type ModeDisruptionTracker struct {
	Mode        TfLMode
	RefreshRate time.Duration
	Service     *ctdf.Service
}

func (d *ModeDisruptionTracker) Run() {
	log.Info().
		Str("mode", d.Mode.ModeID).
		Msg("Registering new mode disruption tracker")

	for {
		startTime := time.Now()

		d.GetDisruptions()

		endTime := time.Now()
		executionDuration := endTime.Sub(startTime)
		waitTime := d.RefreshRate - executionDuration

		if waitTime.Seconds() > 0 {
			time.Sleep(waitTime)
		}
	}
}

func (d *ModeDisruptionTracker) GetDisruptions() {
	requestURL := fmt.Sprintf("https://api.tfl.gov.uk/StopPoint/mode/%s/Disruption?app_key=%s", d.Mode.ModeID, TfLAppKey)
	req, _ := http.NewRequest("GET", requestURL, nil)
	req.Header["user-agent"] = []string{"curl/7.54.1"} // TfL is protected by cloudflare and it gets angry when no user agent is set

	client := &http.Client{}
	resp, err := client.Do(req)

	if err != nil {
		log.Fatal().Err(err).Msg("Download file")
	}
	defer resp.Body.Close()

	jsonBytes, _ := io.ReadAll(resp.Body)

	var tflDisruptionData []struct {
		ATCO        string `json:"atcoCode"`
		StationATCO string `json:"stationAtcoCode"`

		FromDate string `json:"fromDate"`
		ToDate   string `json:"toDate"`

		Description string `json:"description"`

		Type       string `json:"type"`
		Appearance string `json:"appearance"`
	}
	json.Unmarshal(jsonBytes, &tflDisruptionData)

	// pretty.Println(tflDisruptionData)
}
