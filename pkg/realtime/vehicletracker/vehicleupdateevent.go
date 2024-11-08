package vehicletracker

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type VehicleUpdateEvent struct {
	LocalID string

	MessageType VehicleUpdateEventType

	IdentifyingInformation map[string]string
	SourceType             string

	VehicleLocationUpdate *VehicleLocationUpdate

	DataSource *ctdf.DataSource
	RecordedAt time.Time
}

type VehicleUpdateEventType string

const (
	VehicleUpdateEventTypeTrip         VehicleUpdateEventType = "Trip"
	VehicleUpdateEventTypeServiceAlert                        = "ServiceAlert"
)

type VehicleLocationUpdate struct {
	Location  ctdf.Location
	Bearing   float64
	Timeframe string

	StopUpdates []VehicleLocationEventStopUpdate

	Occupancy ctdf.RealtimeJourneyOccupancy

	VehicleIdentifier string
}

type VehicleLocationEventStopUpdate struct {
	StopID string

	ArrivalTime   time.Time
	DepartureTime time.Time

	ArrivalOffset   int
	DepartureOffset int
}
