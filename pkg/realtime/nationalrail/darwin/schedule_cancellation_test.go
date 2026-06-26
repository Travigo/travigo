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

	applyDarwinScheduleCancellationState(realtimeJourney, map[string]bool{
		"tmr-stop-cambridge-north": true,
		"tmr-stop-cambridge":       false,
	})

	if !realtimeJourney.Stops["tmr-stop-cambridge-north"].Cancelled {
		t.Fatal("expected Cambridge North cancellation to be applied")
	}
	if realtimeJourney.Stops["tmr-stop-cambridge"].Cancelled {
		t.Fatal("expected stale Cambridge cancellation to be cleared")
	}
}
