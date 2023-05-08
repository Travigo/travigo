package ctdf

import (
	"time"
)

const StopIDFormat = "GB:ATCO:%s"

type Stop struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSource `groups:"internal"`

	PrimaryName    string            `groups:"basic"`
	OtherNames     map[string]string `groups:"basic"`
	TransportTypes []TransportType   `groups:"detailed"`

	Location *Location `groups:"detailed"`

	Services []*Service `bson:"-" groups:"detailed"`

	Active bool `groups:"basic"`

	Associations []*StopAssociation `groups:"detailed"`
}

func (stop *Stop) UpdateNameFromServiceOverrides(service *Service) {
	if service.StopNameOverrides[stop.PrimaryIdentifier] != "" {
		stop.PrimaryName = service.StopNameOverrides[stop.PrimaryIdentifier]
	}
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
