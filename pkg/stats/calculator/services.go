package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type ServicesStats struct {
	Total int

	TransportTypes map[string]int
	Datasources    map[string]int
}

func GetServices() ServicesStats {
	stats := ServicesStats{
		TransportTypes: map[string]int{},
		Datasources:    map[string]int{},
	}
	servicesCollection := database.GetCollection("services")

	numberServices, _ := servicesCollection.CountDocuments(context.Background(), bson.D{})
	stats.Total = int(numberServices)

	stats.TransportTypes = CountAggregate(servicesCollection, "$transporttype")
	stats.Datasources = CountAggregate(servicesCollection, "$datasource.datasetid")

	return stats
}
