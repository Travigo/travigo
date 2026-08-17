package query

import (
	"context"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type JourneyPlan struct {
	Context             context.Context
	OriginStop          *ctdf.Stop
	OriginLocation      *ctdf.Location
	DestinationStop     *ctdf.Stop
	DestinationLocation *ctdf.Location
	Count               int
	StartDateTime       time.Time
	ArrivalByDateTime   time.Time

	MaxChanges                int
	MaxJourneyDuration        time.Duration
	MaxTransferDistanceMetres int
	OriginLocationStopCount   int
	MaxExpandedLabels         int
	MaxSearchDuration         time.Duration

	// ExcludedJourneyRefs and RecoveryAttempt are internal planner controls used
	// when realtime hydration invalidates a scheduled candidate. They are not
	// exposed as public API query parameters.
	ExcludedJourneyRefs []string
	RecoveryAttempt     int
}
