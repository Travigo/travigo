package ctdf

import (
	"time"
)

type Journey struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	DataSource *DataSource

	ServiceRef         string
	OperatorRef        string
	Direction          string
	DepartureTime      time.Time
	DestinationDisplay string

	Availability *Availability

	Path []JourneyPathItem
}

type JourneyPathItem struct {
	OriginStopRef      string
	DestinationStopRef string

	Distance int

	OriginArivalTime      time.Time
	DestinationArivalTime time.Time

	OriginDepartureTime time.Time

	// OriginWaitTime      string
	// DestinationWaitTime string
}
