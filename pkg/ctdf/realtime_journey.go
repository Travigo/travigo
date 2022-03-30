package ctdf

import "time"

var RealtimeJourneyIDFormat = "REALTIME:%s:%s"

type RealtimeJourney struct {
	PrimaryIdentifier string `groups:"basic"`

	JourneyRef string   `groups:"internal"`
	Journey    *Journey `groups:"basic" bson:"-"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	VehicleLocation Location `groups:"basic"`
	VehicleBearing  float64  `groups:"basic"`

	DepartedStopRef string `groups:"basic"`
	DepartedStop    *Stop  `groups:"basic" bson:"-"`

	NextStopRef string `groups:"basic"`
	NextStop    *Stop  `groups:"basic" bson:"-"`

	NextStopArrival   time.Time `groups:"basic"`
	NextStopDeparture time.Time `groups:"basic"`

	Stops map[string]*RealtimeJourneyStops `groups:"basic"` // Historic & future estimates
}

type RealtimeJourneyStops struct {
	StopRef string `groups:"basic"`
	Stop    *Stop  `groups:"basic" bson:"-"`

	ArrivalTime   time.Time `groups:"basic"`
	DepartureTime time.Time `groups:"basic"`

	TimeType RealtimeJourneyStopTimeType `groups:"basic"`
}

type RealtimeJourneyStopTimeType string

const (
	// Unknown         RealtimeJourneyStopTimeType = "Unknown"
	RealtimeJourneyStopTimeHistorical      RealtimeJourneyStopTimeType = "Historical"
	RealtimeJourneyStopTimeEstimatedFuture                             = "EstimatedFuture"
)
