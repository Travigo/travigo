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

	Platforms []*StopPlatform `groups:"detailed"`
	Entrances []*StopEntrance `groups:"detailed"`
}

type StopPlatform struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	PrimaryName string            `groups:"basic"`
	OtherNames  map[string]string `groups:"basic"`

	Location *Location `groups:"detailed"`
}

type StopEntrance struct {
	PrimaryIdentifier string            `groups:"basic"`
	OtherIdentifiers  map[string]string `groups:"basic"`

	PrimaryName string            `groups:"basic"`
	OtherNames  map[string]string `groups:"basic"`

	Location *Location `groups:"detailed"`
}

func (stop *Stop) GetAllStopIDs() []string {
	allStopIDs := []string{
		stop.PrimaryIdentifier,
	}
	for _, platform := range stop.Platforms {
		allStopIDs = append(allStopIDs, platform.PrimaryIdentifier)
	}

	return allStopIDs
}

func (stop *Stop) UpdateNameFromServiceOverrides(service *Service) {
	if service == nil {
		return
	}

	for _, stopID := range stop.GetAllStopIDs() {
		if service.StopNameOverrides[stopID] != "" {
			stop.PrimaryName = service.StopNameOverrides[stopID]

			return
		}
	}
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
