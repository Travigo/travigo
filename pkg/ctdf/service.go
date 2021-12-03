package ctdf

type Service struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     string
	ModificationDateTime string

	DataSource *DataSource

	ServiceName string

	StartDate string
	EndDate   string

	OperatorRef string
	// Operator *Operator

	Routes []Route

	OutboundDescription *ServiceDescription
	InboundDescription  *ServiceDescription
}

type Route struct {
	Description string
}

type ServiceDescription struct {
	Origin      string
	Destination string
	Description string
}
