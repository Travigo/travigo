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
	DeperatureTime     time.Time
	DestinationDisplay string

	Availability *Availability

	Path []JourneyPathItem
}

type JourneyPathItem struct {
	OriginStopRef      string
	DestinationStopRef string

	Distance            float64
	EstimatedTravelTime string
}
