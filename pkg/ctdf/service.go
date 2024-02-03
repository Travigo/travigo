package ctdf

import "time"

type Service struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	ServiceName string `groups:"basic"`

	OperatorRef string `groups:"basic"`
	// Operator *Operator

	Routes []Route `groups:"detailed"`

	BrandColour          string `groups:"basic"`
	SecondaryBrandColour string `groups:"basic"`
	BrandIcon            string `groups:"basic"`
	BrandDisplayMode     string `groups:"basic"`

	StopNameOverrides map[string]string `groups:"internal"`

	TransportType TransportType `groups:"basic"`
}

type Route struct {
	Origin      string `groups:"basic"`
	Destination string `groups:"basic"`
	Description string `groups:"basic"`
}
