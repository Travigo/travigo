package ctdf

import "time"

type JourneyPlanResults struct {
	JourneyPlans []JourneyPlan `groups:"basic,detailed"`

	OriginStop      Stop `groups:"basic,detailed"`
	DestinationStop Stop `groups:"basic,detailed"`
}

type JourneyPlan struct {
	RouteItems []JourneyPlanRouteItem `groups:"basic,detailed"`

	StartTime   time.Time     `groups:"basic,detailed"`
	ArrivalTime time.Time     `groups:"basic,detailed"`
	Duration    time.Duration `groups:"basic,detailed"`
}

type JourneyPlanRouteItem struct {
	Journey Journey `groups:"basic,detailed"`

	JourneyType DepartureBoardRecordType `groups:"basic,detailed"`

	OriginStopRef      string `groups:"basic,detailed"`
	DestinationStopRef string `groups:"basic,detailed"`

	StartTime   time.Time `groups:"basic,detailed"`
	ArrivalTime time.Time `groups:"basic,detailed"`
}
