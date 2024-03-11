package vehicletracker

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type VehicleLocationEvent struct {
	LocalID string

	IdentifyingInformation map[string]string
	SourceType             string

	Location  ctdf.Location
	Bearing   float64
	Timeframe string

	StopUpdates []VehicleLocationEventStopUpdate

	VehicleIdentifier string

	DataSource *ctdf.DataSource
	RecordedAt time.Time
}

type VehicleLocationEventStopUpdate struct {
	StopID string

	ArrivalTime   time.Time
	DepartureTime time.Time

	Offset int
}
