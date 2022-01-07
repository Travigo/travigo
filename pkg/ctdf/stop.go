package ctdf

import (
	"context"
	"time"

	"github.com/britbus/britbus/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const StopIDFormat = "GB:ATCO:%s"

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

	Services []*Service `bson:"-"`

	Associations []*StopAssociation
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
