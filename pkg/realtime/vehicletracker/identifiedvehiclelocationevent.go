package vehicletracker

import (
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataimporter/siri_vm"
)

type IdentifiedVehicleLocationEvent struct {
	JourneyRef  string
	ServiceRef  string
	OperatorRef string

	CreationDateTime time.Time `groups:"detailed"`

	DataSource *ctdf.DataSource `groups:"internal"`

	Timeframe string

	VehicleLocation ctdf.Location `groups:"basic"`
	VehicleBearing  float64
	VehicleRef      string

	SiriVMActivity *siri_vm.VehicleActivity
}
