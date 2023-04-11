package ctdf

import (
	"context"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
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

func (s *Stop) GetServices() {
	servicesCollection := database.GetCollection("services")
	journeysCollection := database.GetCollection("journeys")

	filter := bson.M{"$or": bson.A{
		bson.M{"path.originstopref": s.PrimaryIdentifier},
		bson.M{"path.destinationstopref": s.PrimaryIdentifier},
	},
	}

	results, _ := journeysCollection.Distinct(context.Background(), "serviceref", filter)

	// TODO: Can probably do this without the for loop
	for _, serviceID := range results {
		var service *Service
		servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": serviceID}).Decode(&service)

		s.Services = append(s.Services, service)
	}

	s.RecalculateTransportTypes()
}

func (s *Stop) RecalculateTransportTypes() {
	if len(s.Services) == 0 {
		return
	}

	transportTypes := []TransportType{}
	presentTransportTypes := make(map[TransportType]bool)

	for _, service := range s.Services {
		if service.TransportType != "" && !presentTransportTypes[service.TransportType] {
			transportTypes = append(transportTypes, service.TransportType)
			presentTransportTypes[service.TransportType] = true
		}
	}

	s.TransportTypes = transportTypes
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
