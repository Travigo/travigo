package databaselookup

import (
	"context"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func (s Source) ServicesByStopQuery(q query.ServicesByStop) ([]*ctdf.Service, error) {
	servicesCollection := database.GetCollection("services")
	journeysCollection := database.GetCollection("journeys")

	// Contains the stops primary id and all platforms primary ids
	allStopIDs := q.Stop.GetAllStopIDs()
	filter := bson.M{
		"$or": bson.A{
			bson.M{"path.originstopref": bson.M{"$in": allStopIDs}},
			bson.M{"path.destinationstopref": bson.M{"$in": allStopIDs}},
		},
	}

	results, _ := journeysCollection.Distinct(context.Background(), "serviceref", filter)
	var services []*ctdf.Service

	// TODO: Can probably do this without the for loop
	for _, serviceID := range results {
		var service *ctdf.Service
		servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": serviceID}).Decode(&service)

		if service != nil {
			services = append(services, service)
		}
	}

	if len(services) == 0 {
		return nil, source.UnsupportedSourceError
	}

	return services, nil
}
