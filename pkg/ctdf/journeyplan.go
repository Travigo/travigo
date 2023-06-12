package ctdf

import "time"

type JourneyPlan struct {
	RouteItems []JourneyPlanRouteItem

	StartTime   time.Time
	ArrivalTime time.Time
	Duration    time.Duration
}

type JourneyPlanRouteItem struct {
	Journey Journey

	OriginStopRef      string
	DestinationStopRef string

	StartTime   time.Time
	ArrivalTime time.Time
}
