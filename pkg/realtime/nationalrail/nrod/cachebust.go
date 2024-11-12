package nrod

import (
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/util"
)

// TODO this should probably live somewhere else
func cacheBustJourney(journey *ctdf.Journey) {
	var stopIDs []string
	for _, pathItem := range journey.Path {
		pathItem.GetOriginStop()
		pathItem.GetDestinationStop()

		stopIDs = append(stopIDs, pathItem.OriginStop.PrimaryIdentifier)
		stopIDs = append(stopIDs, pathItem.DestinationStop.PrimaryIdentifier)
	}

	stopIDs = util.RemoveDuplicateStrings(stopIDs, []string{})

	for _, stopID := range stopIDs {
		cacheItemPath := fmt.Sprintf("cachedresults/departureboardjourneys/%s/*", stopID)
		cachedresults.DeletePrefix(cacheItemPath)
	}
}
