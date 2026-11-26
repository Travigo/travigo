package ctdf

import "time"

type JourneyPlanRouteItemType string

const (
	JourneyPlanRouteItemTypeJourney  JourneyPlanRouteItemType = "Journey"
	JourneyPlanRouteItemTypeTransfer JourneyPlanRouteItemType = "Transfer"
)

type JourneyPlanResults struct {
	JourneyPlans []JourneyPlan `groups:"basic,detailed,web-planner"`

	OriginStop      Stop `groups:"basic,detailed,web-planner"`
	DestinationStop Stop `groups:"basic,detailed,web-planner"`

	SearchTruncated       bool   `groups:"basic,detailed,web-planner"`
	SearchTruncatedReason string `groups:"basic,detailed,web-planner" json:",omitempty"`
	ExpandedLabels        int    `groups:"detailed,web-planner" json:",omitempty"`
	SearchDurationMillis  int64  `groups:"detailed,web-planner" json:",omitempty"`
	FirstPlanMillis       int64  `groups:"detailed,web-planner" json:",omitempty"`
}

type JourneyPlan struct {
	RouteItems []JourneyPlanRouteItem `groups:"basic,detailed,web-planner"`
	Fare       *FareQuote             `groups:"basic,web-planner" json:",omitempty"`

	StartTime   time.Time     `groups:"basic,detailed,web-planner"`
	ArrivalTime time.Time     `groups:"basic,detailed,web-planner"`
	Duration    time.Duration `groups:"basic,detailed,web-planner"`
}

type JourneyPlanRouteItem struct {
	Type JourneyPlanRouteItemType `groups:"basic,detailed,web-planner"`

	Journey *Journey `groups:"basic,detailed,web-planner" json:",omitempty"`

	JourneyType                 DepartureBoardRecordType `groups:"basic,detailed,web-planner" json:",omitempty"`
	TransferType                StopTransferType         `groups:"basic,detailed,web-planner" json:",omitempty"`
	JourneyOriginStopIndex      int                      `json:"-" bson:"-"`
	JourneyDestinationStopIndex int                      `json:"-" bson:"-"`

	OriginStopRef      string `groups:"basic,detailed,web-planner"`
	DestinationStopRef string `groups:"basic,detailed,web-planner"`
	OriginStop         *Stop  `groups:"web-planner" bson:"-" json:",omitempty"`
	DestinationStop    *Stop  `groups:"web-planner" bson:"-" json:",omitempty"`

	StartTime            time.Time `groups:"basic,detailed,web-planner"`
	ArrivalTime          time.Time `groups:"basic,detailed,web-planner"`
	ScheduledStartTime   time.Time `json:"-" bson:"-"`
	ScheduledArrivalTime time.Time `json:"-" bson:"-"`

	DistanceMetres           int `groups:"basic,detailed,web-planner" json:",omitempty"`
	WalkDurationSeconds      int `groups:"basic,detailed,web-planner" json:",omitempty"`
	MinChangeDurationSeconds int `groups:"basic,detailed,web-planner" json:",omitempty"`
	TotalDurationSeconds     int `groups:"basic,detailed,web-planner" json:",omitempty"`
}
