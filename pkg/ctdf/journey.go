package ctdf

type Journey struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	DataSource *DataSource

	ServiceRef         string
	OperatorRef        string
	Direction          string
	DeperatureTime     string // should be some sort of time type
	DestinationDisplay string

	Availability *Availability

	Path []JourneyPathItem
}

type JourneyPathItem struct {
	OriginStopRef      string
	DestinationStopRef string

	Distance            string
	EstimatedTravelTime string
}
