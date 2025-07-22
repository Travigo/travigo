package ctdf

import (
	"io"
	"time"
)

type Service struct {
	PrimaryIdentifier string   `groups:"basic,search,search-llm,stop-llm,departures-llm"`
	OtherIdentifiers  []string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSourceReference `groups:"detailed"`

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

// Still not perfect as something like st pancras actually covers multiple coordinates
func (service *Service) GenerateDeterministicID(writer io.Writer) {
	writer.Write([]byte(service.OperatorRef))
	writer.Write([]byte(service.ServiceName))
	writer.Write([]byte(service.TransportType))
}

func (service *Service) GetPrimaryIdentifier() string {
	return service.PrimaryIdentifier
}
func (service *Service) GetCreationDateTime() time.Time {
	return service.CreationDateTime
}
func (service *Service) SetPrimaryIdentifier(id string) {
	service.PrimaryIdentifier = id
}
func (service *Service) SetOtherIdentifiers(ids []string) {
	service.OtherIdentifiers = ids
}
