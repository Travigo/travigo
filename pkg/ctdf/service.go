package ctdf

import "time"

type Service struct {
	PrimaryIdentifier string   `groups:"basic,search,search-llm,stop-llm,departures-llm"`
	OtherIdentifiers  []string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	ServiceName string `groups:"basic,search,search-llm,stop-llm,departures-llm"`

	OperatorRef string `groups:"basic"`
	// Operator *Operator

	Routes []Route `groups:"detailed"`

	BrandColour          string `groups:"basic,search"`
	SecondaryBrandColour string `groups:"basic,search"`
	BrandIcon            string `groups:"basic,search"`
	BrandDisplayMode     string `groups:"basic,search"`

	StopNameOverrides map[string]string `groups:"internal"`

	TransportType TransportType `groups:"basic,search,search-llm,stop-llm,departures-llm"`
}

type Route struct {
	Origin      string `groups:"basic"`
	Destination string `groups:"basic"`
	Description string `groups:"basic"`
}
