package tflarrivals

import (
	"sync"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/util"
)

var TfLAppKey string

type TrackerManager struct {
	Modes []*TfLMode

	RuntimeJourneyFilter func(string, string) bool
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
					Dur("refresharrivals", mode.ArrivalRefreshRate).
					Msg("Loaded line for tracker")
			}

			go func(mode *TfLMode) {
				modeArrivalTracker := ModeArrivalTracker{
					Mode:        mode,
					RefreshRate: mode.ArrivalRefreshRate,

					RuntimeJourneyFilter: t.RuntimeJourneyFilter,
				}

				modeArrivalTracker.Run(getRoutes)
			}(mode)
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
