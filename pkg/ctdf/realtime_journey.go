package ctdf

import "time"

var RealtimeJourneyIDFormat = "REALTIME:%s:%s"

type RealtimeJourney struct {
	PrimaryIdentifier string

	JourneyRef string   `groups:"internal"`
	Journey    *Journey `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	VehicleLocation Location `groups:"basic"`
	VehicleBearing  float64  `groups:"basic"`

	DepartedStopRef string `groups:"basic"`
	DepartedStop    *Stop  `groups:"basic"`

	NextStopRef string `groups:"basic"`
	NextStop    *Stop  `groups:"basic"`

	StopHistory []*RealtimeJourneyStopHistory `groups:"basic"`
}

type RealtimeJourneyStopHistory struct {
	StopRef string
	// Stop    *Stop

	ArrivalTime   time.Time
	DepartureTime time.Time
}
