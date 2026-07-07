package query

import (
	"github.com/travigo/travigo/pkg/ctdf"
	"time"
)

type JourneyPlan struct {
	OriginStop      *ctdf.Stop
	DestinationStop *ctdf.Stop
	Count           int
	StartDateTime   time.Time

	MaxChanges                 int
	MaxJourneyDuration         time.Duration
	MaxTransferDistanceMetres  int
	DepartureBoardCountPerStop int
	OriginDepartureBoardCount  int
	MaxExpandedLabels          int
	MaxSearchDuration          time.Duration
}
