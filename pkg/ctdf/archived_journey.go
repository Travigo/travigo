package ctdf

import "time"

type ArchivedJourney struct {
	PrimaryIdentifier string `groups:"basic"`

	JourneyRef string `groups:"internal"`

	ServiceRef string `groups:"internal"`

	OperatorRef string `groups:"internal"`
	// Journey    *Journey `groups:"basic" bson:"-"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	Stops []*ArchivedJourneyStops `groups:"basic"`

	Reliability RealtimeJourneyReliabilityType `groups:"basic"`

	VehicleRef string `groups:"internal"`
}

type ArchivedJourneyStops struct {
	StopRef string `groups:"basic"`
	// Stop    *Stop  `groups:"basic" bson:"-"`

	ExpectedArrivalTime   time.Time `groups:"basic"`
	ExpectedDepartureTime time.Time `groups:"basic"`

	HasActualData bool `groups:"basic"`

	ActualArrivalTime   time.Time `groups:"basic"`
	ActualDepartureTime time.Time `groups:"basic"`

	Offset int `groups:"basic"`
}
