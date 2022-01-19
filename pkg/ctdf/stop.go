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

	PrimaryName string            `groups:"basic"`
	OtherNames  map[string]string `groups:"basic"`
	Type        string            `groups:"detailed"` // or an enum
	Status      string            `groups:"detailed"`

	Location *Location `groups:"detailed"`

	Services []*Service `bson:"-" groups:"detailed"`

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
}

type StopAssociation struct {
	Type                 string
	AssociatedIdentifier string
}
