package tflarrivals

import (
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/util"
	"sync"
	"time"
)

type TrackerManager struct {
	Lines []TfLLine
}

type TfLLine struct {
	LineID        string
	LineName      string
	TransportType ctdf.TransportType
}

func (t TrackerManager) Run() {
	log.Info().Msg("Starting TfL Arrivals Tracker")

	env := util.GetEnvironmentVariables()

	if env["TRAVIGO_TFL_API_KEY"] == "" {
		log.Fatal().Msg("\"TRAVIGO_TFL_API_KEY\" not set in environment")
	}

	// TODO replace with proper cache
	stopTflStopCacheMutex = sync.Mutex{}
	stopTflStopCache = map[string]*ctdf.Stop{}

	for _, line := range t.Lines {
		go func(line TfLLine) {
			lineTracker := LineTracker{
				Line:        line,
				RefreshRate: time.Second * 15,
				TfLAppKey:   env["TRAVIGO_TFL_API_KEY"],
			}

			lineTracker.Run()
		}(line)
	}
}
