package darwin

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/util"
)

func darwinJourneyStopOccurrenceIndex(journey *ctdf.Journey, stop *ctdf.Stop, occurrence int) int {
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
