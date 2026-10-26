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
	for index, path := range journey.Path {
		if path.OriginStopRef == stop.PrimaryIdentifier || util.ContainsString(stop.OtherIdentifiers, path.OriginStopRef) {
			if seen == occurrence {
				return index
			}
			seen++
		}
	}
	if len(journey.Path) > 0 {
		ref := journey.Path[len(journey.Path)-1].DestinationStopRef
		if (ref == stop.PrimaryIdentifier || util.ContainsString(stop.OtherIdentifiers, ref)) && seen == occurrence {
			return len(journey.Path)
		}
	}
	return -1
}
