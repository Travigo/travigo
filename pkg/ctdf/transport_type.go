package ctdf

type TransportType string

//goland:noinspection GoUnusedConst
const (
	TransportTypeBus      TransportType = "Bus"
	TransportTypeCoach                  = "Coach"
	TransportTypeTram                   = "Tram"
	TransportTypeTaxi                   = "Taxi"
	TransportTypeTrain                  = "Train"
	TransportTypeMetro                  = "Metro"
	TransportTypeBoat                   = "Boat"
	TransportTypeAirport                = "Airport"
	TransportTypeCableCar               = "CableCar"
	TransportTypeUnknown                = "UNKNOWN"
)