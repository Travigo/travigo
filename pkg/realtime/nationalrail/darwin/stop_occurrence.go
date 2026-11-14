package darwin

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/util"
)

func darwinJourneyStopOccurrenceIndex(journey *ctdf.Journey, stop *ctdf.Stop, occurrence int) int {
	// Performance Review [Set J] [PR106] [P2] (realtime): Every reported location scans the route
	// and its aliases again, making a full status update quadratic in route length.
	// Build canonical-stop -> ordered occurrence indices once per route version
	// (or at least per status), including the terminal call and alias resolution.
	// Preserve occurrence selection on loops rather than indexing one call per stop.
	seen := 0
	for index, call := range journey.StopCalls() {
		if call.StopRef == stop.PrimaryIdentifier || util.ContainsString(stop.OtherIdentifiers, call.StopRef) {
			if seen == occurrence {
				return index
			}
			seen++
		}
	}
	return -1
}
