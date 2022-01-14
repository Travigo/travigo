package ctdf

type Service struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     string `groups:"detailed"`
	ModificationDateTime string `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	ServiceName string `groups:"basic"`

	OperatorRef string `groups:"internal"`
	// Operator *Operator

	Routes []Route `groups:"detailed"`

	OutboundDescription *ServiceDescription `groups:"basic"`
	InboundDescription  *ServiceDescription `groups:"basic"`
}

type Route struct {
	Description string
}

type ServiceDescription struct {
	Origin      string `groups:"basic"`
	Destination string `groups:"basic"`
	Description string `groups:"basic"`
}
