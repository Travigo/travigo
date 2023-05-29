package tflarrivals

import (
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
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

	for _, line := range t.Lines {
		go func(line TfLLine) {
			lineTracker := LineTracker{
				Line:        line,
				RefreshRate: time.Second * 10,
			}

			lineTracker.Run()
		}(line)
	}
}
