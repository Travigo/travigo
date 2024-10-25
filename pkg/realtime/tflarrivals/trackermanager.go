package tflarrivals

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
)

var TfLAppKey string

type TrackerManager struct {
	Modes []*TfLMode

	RuntimeLineFilter    func(string) bool
	RuntimeJourneyFilter func(string, string) bool
}

type TfLLine struct {
	LineID        string
	LineName      string
	TransportType ctdf.TransportType

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

type TfLMode struct {
	ModeID        string
	TransportType ctdf.TransportType

	Lines []*TfLLine

	TrackArrivals    bool
	TrackDisruptions bool

	ArrivalRefreshRate    time.Duration
	DisruptionRefreshRate time.Duration
}

func (m *TfLMode) GetLines() {
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

		m.Lines = append(m.Lines, tflLine)
	}
}

func (t TrackerManager) Run(getRoutes bool) {
	log.Info().Msg("Starting TfL Arrivals & Distuptions Tracker")

	env := util.GetEnvironmentVariables()

	if env["TRAVIGO_TFL_API_KEY"] == "" {
		log.Fatal().Msg("\"TRAVIGO_TFL_API_KEY\" not set in environment")
	}
	TfLAppKey = env["TRAVIGO_TFL_API_KEY"]

	// TODO replace with proper cache
	stopTflStopCacheMutex = sync.Mutex{}
	stopTflStopCache = map[string]*ctdf.Stop{}

	for _, mode := range t.Modes {
		log.Info().
			Str("mode", mode.ModeID).
			Str("type", string(mode.TransportType)).
			Bool("trackarrivals", mode.TrackArrivals).
			Bool("trackdisruptions", mode.TrackDisruptions).
			Dur("refresharrivals", mode.ArrivalRefreshRate).
			Dur("refreshdisrptions", mode.DisruptionRefreshRate).
			Msg("Registered mode")

		mode.GetLines()
		log.Info().Str("mode", mode.ModeID).Int("lines", len(mode.Lines)).Msg("Retrieved modes lines")

		if mode.TrackArrivals {
			for _, line := range mode.Lines {
				log.Info().
					Str("mode", mode.ModeID).
					Str("line", line.LineID).
					Bool("trackarrivals", mode.TrackArrivals).
					Dur("refresharrivals", mode.ArrivalRefreshRate).
					Msg("Starting line trackers")

				go func(line *TfLLine, mode *TfLMode) {
					lineArrivalTracker := LineArrivalTracker{
						Line:        line,
						RefreshRate: mode.ArrivalRefreshRate,

						RuntimeLineFilter:    t.RuntimeLineFilter,
						RuntimeJourneyFilter: t.RuntimeJourneyFilter,
					}

					lineArrivalTracker.Run(getRoutes)
				}(line, mode)
			}
		}

		if mode.TrackDisruptions {
			log.Info().
				Str("mode", mode.ModeID).
				Dur("refreshdisruptions", mode.DisruptionRefreshRate).
				Msg("Starting mode trackers")

			go func(mode *TfLMode) {
				modeDisruptionTracker := ModeDisruptionTracker{
					Mode:        mode,
					RefreshRate: mode.DisruptionRefreshRate,
				}

				modeDisruptionTracker.Run()
			}(mode)
		}
	}
}
