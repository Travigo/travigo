package darwin

import "github.com/travigo/travigo/pkg/ctdf"

func applyDarwinScheduleCancellationState(realtimeJourney *ctdf.RealtimeJourney, scheduleCancellations map[string]bool) {
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

	for stopRef, cancelled := range scheduleCancellations {
		if realtimeJourney.Stops[stopRef] == nil {
			realtimeJourney.Stops[stopRef] = &ctdf.RealtimeJourneyStops{
				StopRef: stopRef,
			}
		}
		realtimeJourney.Stops[stopRef].StopRef = stopRef
		realtimeJourney.Stops[stopRef].Cancelled = cancelled
	}
}
