package realtime

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

type VehicleLocationEvent struct {
	JourneyRef  string
	ServiceRef  string
	OperatorRef string

	CreationDateTime time.Time `groups:"detailed"`

	DataSource *ctdf.DataSource `groups:"internal"`

	Timeframe string

	VehicleLocation ctdf.Location `groups:"basic"`
	VehicleBearing  float64
	VehicleRef      string
}
