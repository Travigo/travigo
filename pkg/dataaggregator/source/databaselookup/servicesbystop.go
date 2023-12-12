package databaselookup

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s Source) ServicesByStopQuery(q query.ServicesByStop) ([]*ctdf.Service, error) {
	cacheItemPath := fmt.Sprintf("cachedresults/servicesbystopquery/%s", q.Stop.PrimaryIdentifier)
	cachedObject, err := s.CachedResults.Cache.Get(context.Background(), cacheItemPath)

	// Load from cache
	if err == nil {
		var services []*ctdf.Service
		err := json.Unmarshal([]byte(cachedObject), &services)

		if err != nil {
			return nil, err
		}

		return services, nil
	}

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

	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "serviceref", Value: 1},
	})

	serviceFound := map[string]bool{}
	var services []*ctdf.Service

	cursor, _ := journeysCollection.Find(context.Background(), filter, opts)
	for cursor.Next(context.TODO()) {
		var journey struct {
			ServiceRef string
		}
		cursor.Decode(&journey)

		if !serviceFound[journey.ServiceRef] {
			serviceFound[journey.ServiceRef] = true

			var service *ctdf.Service
			servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": journey.ServiceRef}).Decode(&service)

			if service != nil {
				services = append(services, service)
			}
		}
	}

	// Save into cache
	servicesJson, _ := json.Marshal(services)
	s.CachedResults.Cache.Set(context.Background(), cacheItemPath, string(servicesJson))

	return services, nil
}
