package databaselookup

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source/cachedresults"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/transforms"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func (s Source) ServicesByStopQuery(q query.ServicesByStop) ([]*ctdf.Service, error) {
	var services []*ctdf.Service
	// Load from cache
	cacheItemPath := fmt.Sprintf("cachedresults/servicesbystopquery/%s", q.Stop.PrimaryIdentifier)
	services, err := cachedresults.Get[[]*ctdf.Service](s.CachedResults, cacheItemPath)
	if err == nil {
		return services, nil
	}

	// If not in cache then fallback to lookup
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

	serviceRefs, err := journeysCollection.Distinct(context.Background(), "serviceref", filter)

	if err != nil {
		return nil, err
	}

	identifiers := make([]string, 0, len(serviceRefs))
	for _, serviceRef := range serviceRefs {
		if identifier, ok := serviceRef.(string); ok && identifier != "" {
			identifiers = append(identifiers, identifier)
		}
	}
	if len(identifiers) == 0 {
		cachedresults.Set(s.CachedResults, cacheItemPath, services, 24*time.Hour)
		return services, nil
	}

	serviceOpts := options.Find().SetProjection(bson.D{
		bson.E{Key: "creationdatetime", Value: 0},
		bson.E{Key: "modificationdatetime", Value: 0},
		bson.E{Key: "otheridentifiers", Value: 0},
		bson.E{Key: "routes", Value: 0},
		bson.E{Key: "stopnameoverrides", Value: 0},
	})

	cursor, err := servicesCollection.Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": identifiers}},
			bson.M{"otheridentifiers": bson.M{"$in": identifiers}},
		},
	}, serviceOpts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	seenServices := make(map[string]struct{}, len(identifiers))
	for cursor.Next(context.Background()) {
		var service ctdf.Service
		if err := cursor.Decode(&service); err != nil {
			return nil, err
		}
		if _, seen := seenServices[service.PrimaryIdentifier]; seen {
			continue
		}

		transforms.Transform(&service, 1)
		services = append(services, &service)
		seenServices[service.PrimaryIdentifier] = struct{}{}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	cachedresults.Set(s.CachedResults, cacheItemPath, services, 24*time.Hour)

	return services, nil
}
