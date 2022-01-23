package ctdf

import "time"

type RealtimeJourney struct {
	RealtimeJourneyID string

	JourneyRef string   `groups:"internal"`
	Journey    *Journey `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	VehicleLocation Location `groups:"basic"`
	VehicleBearing  float64

	DepartedStopRef string
	DepartedStop    *Stop

	NextStopRef string
	NextStop    *Stop

	StopHistory []*RealtimeJourneyStopHistory
}

type RealtimeJourneyStopHistory struct {
	StopRef string
	// Stop    *Stop

	ArrivalTime   time.Time
	DepartureTime time.Time
}
