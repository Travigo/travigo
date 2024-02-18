package ctdf

type RealtimeJourneyDetailedRail struct {
	Carriages []RailCarriage `groups:"basic"`
}

type RailCarriage struct {
	ID      string               `groups:"basic"`
	Class   string               `groups:"basic"`
	Toilets []RailCarriageToilet `groups:"basic"`

	Occupancy int `groups:"basic"`
}

type RailCarriageToilet struct {
	Type   string `groups:"basic"`
	Status string `groups:"basic"`
}
