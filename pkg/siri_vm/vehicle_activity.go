package siri_vm

type VehicleActivity struct {
	RecordedAtTime string
	ItemIdentifier string
	ValidUntilTime string

	MonitoredVehicleJourney *MonitoredVehicleJourney

	Extensions struct {
		VehicleJourney struct {
			Operational struct {
				TicketMachine struct {
					TicketMachineServiceCode string
					JourneyCode              string
				}
			}

			VehicleUniqueId     string
			SeatedOccupancy     int
			SeatedCapacity      int
			WheelchairOccupancy int
			WheelchairCapacity  int
			OccupancyThresholds string
		}
	}
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
	Bearing   float64
	Occupancy string

	BlockRef   string
	VehicleRef string
}
