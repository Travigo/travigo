package darwin

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestApplyDarwinScheduleCancellationStateClearsStaleCancellations(t *testing.T) {
	realtimeJourney := &ctdf.RealtimeJourney{
		Stops: map[string]*ctdf.RealtimeJourneyStops{
			"tmr-stop-cambridge-north": {
				StopRef: "tmr-stop-cambridge-north",
			},
			"tmr-stop-cambridge": {
				StopRef:   "tmr-stop-cambridge",
				Cancelled: true,
			},
		},
	}

	applyDarwinScheduleCancellationState(realtimeJourney, map[string]darwinScheduleCancellation{
		"north":     {stopRef: "tmr-stop-cambridge-north", cancelled: true},
		"cambridge": {stopRef: "tmr-stop-cambridge", cancelled: false},
	})

	if !realtimeJourney.RealtimeStop("tmr-stop-cambridge-north", 0).Cancelled {
		t.Fatal("expected Cambridge North cancellation to be applied")
	}
	if realtimeJourney.RealtimeStop("tmr-stop-cambridge", 0).Cancelled {
		t.Fatal("expected stale Cambridge cancellation to be cleared")
	}
}
