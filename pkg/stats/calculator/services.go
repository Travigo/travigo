package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type ServicesStats struct {
	Total int
}

func GetServices() ServicesStats {
	servicesCollection := database.GetCollection("services")
	numberServices, _ := servicesCollection.CountDocuments(context.Background(), bson.D{})

	return ServicesStats{
		Total: int(numberServices),
	}
}
