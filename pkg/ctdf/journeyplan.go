package ctdf

import "time"

type JourneyPlanResults struct {
	JourneyPlans []JourneyPlan

	OriginStop      Stop
	DestinationStop Stop
}

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
