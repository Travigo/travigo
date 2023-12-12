package databaselookup

import (
	"context"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
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

	// now := time.Now()

	// results, _ := journeysCollection.Distinct(context.Background(), "serviceref", filter)
	// var services []*ctdf.Service

	// // TODO: Can probably do this without the for loop
	// for _, serviceID := range results {
	// 	var service *ctdf.Service
	// 	servicesCollection.FindOne(context.Background(), bson.M{"primaryidentifier": serviceID}).Decode(&service)

	// 	if service != nil {
	// 		services = append(services, service)
	// 	}
	// }

	// log.Info().Str("length", time.Now().Sub(now).String()).Msg("okayy")

	// now = time.Now()

	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "serviceref", Value: 1},
	})

	serviceFound := map[string]bool{}
	var services2 []*ctdf.Service

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
				services2 = append(services2, service)
			}
		}
	}

	// log.Info().Str("length", time.Now().Sub(now).String()).Msg("okayy2")

	return services2, nil
}
