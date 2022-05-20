package siri_vm

type VehicleActivity struct {
	RecordedAtTime string
	ItemIdentifier string
	ValidUntilTime string

	MonitoredVehicleJourney *MonitoredVehicleJourney
}

type MonitoredVehicleJourney struct {
	LineRef           string
	DirectionRef      string
	PublishedLineName string

	FramedVehicleJourneyRef struct {
		DataFrameRef           string
		DatedVehicleJourneyRef string
	}

	VehicleJourneyRef string

	OperatorRef string

	OriginRef  string
	OriginName string

	DestinationRef           string
	DestinationName          string
	OriginAimedDepartureTime string

	VehicleLocation struct {
		Longitude float64
		Latitude  float64
	}
	Bearing float64

	BlockRef   string
	VehicleRef string
}
