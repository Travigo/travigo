package darwin

import "github.com/travigo/travigo/pkg/ctdf"

type darwinScheduleCancellation struct {
	stopRef          string
	journeyStopIndex int
	cancelled        bool
}

func applyDarwinScheduleCancellationState(realtimeJourney *ctdf.RealtimeJourney, scheduleCancellations map[string]darwinScheduleCancellation) {
	if realtimeJourney.Stops == nil {
		realtimeJourney.Stops = map[string]*ctdf.RealtimeJourneyStops{}
	}

	for stopRef, realtimeStop := range realtimeJourney.Stops {
		if realtimeStop == nil {
			continue
		}
		if realtimeStop.StopRef == "" {
			realtimeStop.StopRef = stopRef
		}
		realtimeStop.Cancelled = false
	}

	for _, cancellation := range scheduleCancellations {
		stop := realtimeJourney.RealtimeStop(cancellation.stopRef, cancellation.journeyStopIndex)
		if stop == nil {
			stop = &ctdf.RealtimeJourneyStops{StopRef: cancellation.stopRef, JourneyStopIndex: cancellation.journeyStopIndex}
		}
		stop.Cancelled = cancellation.cancelled
		realtimeJourney.SetRealtimeStop(stop)
	}
}
