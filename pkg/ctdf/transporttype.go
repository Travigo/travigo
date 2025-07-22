package ctdf

type TransportType string

//goland:noinspection GoUnusedConst
const (
	TransportTypeBus       TransportType = "Bus"
	TransportTypeCoach     TransportType = "Coach"
	TransportTypeTram      TransportType = "Tram"
	TransportTypeTaxi      TransportType = "Taxi"
	TransportTypeRail      TransportType = "Rail"
	TransportTypeMetro     TransportType = "Metro"
	TransportTypeFerry     TransportType = "Ferry"
	TransportTypeAirport   TransportType = "Airport"
	TransportTypeCableCar  TransportType = "CableCar"
	TransportTypeFunicular TransportType = "Funicular"
	TransportTypeUnknown   TransportType = "UNKNOWN"
)
