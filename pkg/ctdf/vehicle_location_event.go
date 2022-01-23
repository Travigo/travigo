package ctdf

import "time"

type VehicleLocationEvent struct {
	IdentifyingInformation map[string]string

	CreationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	VehicleLocation Location `groups:"basic"`
	VehicleBearing  float64
}
