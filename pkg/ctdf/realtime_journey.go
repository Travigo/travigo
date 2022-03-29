package ctdf

import "time"

var RealtimeJourneyIDFormat = "REALTIME:%s:%s"

type RealtimeJourney struct {
	PrimaryIdentifier string

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

	Stops []*RealtimeJourneyStops `groups:"basic"` // Historic & future estimates
}

type RealtimeJourneyStops struct {
	StopRef string
	// Stop    *Stop

	ArrivalTime   time.Time
	DepartureTime time.Time
}
