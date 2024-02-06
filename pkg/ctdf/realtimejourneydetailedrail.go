package ctdf

type RealtimeInformationDetailedRail struct {
	Carriages []RailCarriage
}

type RailCarriage struct {
	ID      string
	Class   string
	Toilets []RailCarriageToilet

	Occupancy int
}

type RailCarriageToilet struct {
	Type   string
	Status string
}
