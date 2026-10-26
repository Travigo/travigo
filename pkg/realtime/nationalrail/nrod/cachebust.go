package nrod

import (
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/util"
)

// TODO this should probably live somewhere else
func cacheBustJourney(journey *ctdf.Journey) {
	// Performance Review [Set I] [PR111] [P2] (realtime): Unhydrated legs can fetch the same stops
	// before deduplication; each unique stop then invokes two DeletePrefix calls.
	// Each SCANs the keyspace and deletes matches individually, so N stops cause
	// 2N keyspace traversals per update. Deduplicate refs before batch resolution and
	// use one bounded invalidation pass, reverse indices, or per-stop cache versions;
	// include canonical/alias board keys and pipeline bounded deletion batches.
	var stopIDs []string
	for _, pathItem := range journey.Path {
		pathItem.GetOriginStop()
		pathItem.GetDestinationStop()

		stopIDs = append(stopIDs, pathItem.OriginStop.PrimaryIdentifier)
		stopIDs = append(stopIDs, pathItem.DestinationStop.PrimaryIdentifier)
	}

	stopIDs = util.RemoveDuplicateStrings(stopIDs, []string{})

	for _, stopID := range stopIDs {
		cachedresults.DeletePrefix(fmt.Sprintf("cachedresults/departureboardjourneys/%s/*", stopID))
		cachedresults.DeletePrefix(fmt.Sprintf("cachedresults/arrivalboardjourneys/%s/*", stopID))
	}
}
