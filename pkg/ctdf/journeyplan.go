package ctdf

import "time"

type JourneyPlanRouteItemType string

const (
	JourneyPlanRouteItemTypeJourney  JourneyPlanRouteItemType = "Journey"
	JourneyPlanRouteItemTypeTransfer JourneyPlanRouteItemType = "Transfer"
)

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
	Type JourneyPlanRouteItemType `groups:"basic,detailed"`

	Journey *Journey `groups:"basic,detailed" json:",omitempty"`

	JourneyType  DepartureBoardRecordType `groups:"basic,detailed" json:",omitempty"`
	TransferType StopTransferType         `groups:"basic,detailed" json:",omitempty"`

	OriginStopRef      string `groups:"basic,detailed"`
	DestinationStopRef string `groups:"basic,detailed"`

	StartTime   time.Time `groups:"basic,detailed"`
	ArrivalTime time.Time `groups:"basic,detailed"`

	DistanceMetres           int `groups:"basic,detailed" json:",omitempty"`
	WalkDurationSeconds      int `groups:"basic,detailed" json:",omitempty"`
	MinChangeDurationSeconds int `groups:"basic,detailed" json:",omitempty"`
	TotalDurationSeconds     int `groups:"basic,detailed" json:",omitempty"`
}
