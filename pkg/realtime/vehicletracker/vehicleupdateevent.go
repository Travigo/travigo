package vehicletracker

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type VehicleUpdateEvent struct {
	LocalID string

	MessageType VehicleUpdateEventType

	SourceType string

	VehicleLocationUpdate *VehicleLocationUpdate
	ServiceAlertUpdate    *ServiceAlertUpdate

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

	IdentifyingInformation map[string]string

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

type ServiceAlertUpdate struct {
	Type ctdf.ServiceAlertType

	Title       string
	Description string

	ValidFrom  time.Time
	ValidUntil time.Time

	IdentifyingInformation map[string]string
}
