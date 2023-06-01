package dataimporter

import (
	"context"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

func squashIdenticalServices(filter bson.M) {
	servicesCollection := database.GetCollection("services")
	journeysCollection := database.GetCollection("journeys")

	cursor, _ := servicesCollection.Find(context.Background(), filter)

	var services []ctdf.Service
	if err := cursor.All(context.Background(), &services); err != nil {
		log.Error().Err(err).Msg("Squash query failed")
	}

	serviceNameIDs := map[string][]string{}
	for _, service := range services {
		serviceNameIDs[service.ServiceName] = append(serviceNameIDs[service.ServiceName], service.PrimaryIdentifier)
	}

	for _, ids := range serviceNameIDs {
		if len(ids) > 1 {
			primaryID := ids[0]
			removeIDs := ids[1:]

			journeysCollection.UpdateMany(
				context.Background(),
				bson.M{"serviceref": bson.M{"$in": removeIDs}},
				bson.M{"$set": bson.M{"serviceref": primaryID}},
			)

			servicesCollection.DeleteMany(context.Background(), bson.M{"primaryidentifier": bson.M{"$in": removeIDs}})
		}
	}
}
