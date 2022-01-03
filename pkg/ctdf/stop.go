package ctdf

import "time"

const StopIDFormat = "UK:ATCO:%s"

type Stop struct {
	PrimaryIdentifier string
	OtherIdentifiers  map[string]string

	CreationDateTime     time.Time
	ModificationDateTime time.Time

	DataSource *DataSource

	PrimaryName string
	OtherNames  map[string]string
	Type        string // or an enum
	Status      string

	Location *Location

	Associations []StopAssociation
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
