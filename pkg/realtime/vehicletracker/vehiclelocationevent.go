package vehicletracker

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type VehicleLocationEvent struct {
	IdentifyingInformation map[string]string
	SourceType             string

	Location  ctdf.Location
	Bearing   float64
	Timeframe string

	VehicleIdentifier string

	DataSource *ctdf.DataSource
	RecordedAt time.Time
}
