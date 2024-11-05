package ctdf

type TransportType string

//goland:noinspection GoUnusedConst
const (
	TransportTypeBus       TransportType = "Bus"
	TransportTypeCoach                   = "Coach"
	TransportTypeTram                    = "Tram"
	TransportTypeTaxi                    = "Taxi"
	TransportTypeRail                    = "Rail"
	TransportTypeMetro                   = "Metro"
	TransportTypeFerry                   = "Ferry"
	TransportTypeAirport                 = "Airport"
	TransportTypeCableCar                = "CableCar"
	TransportTypeFunicular               = "Funicular"
	TransportTypeUnknown                 = "UNKNOWN"
)
