package ctdf

import "time"

type VehicleLocationEvent struct {
	JourneyRef string

	CreationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	Timeframe string

	VehicleLocation Location `groups:"basic"`
	VehicleBearing  float64
}
