package ctdf

import (
	"io"
	"time"
)

type Service struct {
	PrimaryIdentifier string   `groups:"basic,search,search-llm,stop-llm,departures-llm,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved,web-notification"`
	OtherIdentifiers  []string `groups:"basic"`

	CreationDateTime     time.Time `groups:"detailed"`
	ModificationDateTime time.Time `groups:"detailed"`

	DataSource *DataSourceReference `groups:"detailed,web-stop-detail,web-journey,web-service-detail"`

	ServiceName string `groups:"basic,search,search-llm,stop-llm,departures-llm,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved,web-notification"`
	Description string `groups:"detailed" bson:",omitempty"`
	Website     string `groups:"detailed" bson:",omitempty"`
	NetworkRef  string `groups:"detailed" bson:",omitempty"`

	OperatorRef string `groups:"basic,web-service-detail"`
	// Operator *Operator

	Routes []Route `groups:"detailed"`

	BrandColour          string `groups:"basic,search,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved"`
	SecondaryBrandColour string `groups:"basic,search,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved"`
	BrandIcon            string `groups:"basic,search,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved"`
	BrandDisplayMode     string `groups:"basic,search,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved"`

	StopNameOverrides map[string]string `groups:"internal"`

	TransportType TransportType `groups:"basic,search,search-llm,stop-llm,departures-llm,web-stop-map,web-stop-search,web-stop-summary,web-stop-detail,web-board,web-journey,web-planner,web-service-summary,web-service-detail,web-saved,web-notification"`
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
