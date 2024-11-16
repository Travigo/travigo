package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type ServicesStats struct {
	Total int

	TransportTypes map[string]int
	Datasets       map[string]int
	Providers      map[string]int
	Countries      map[string]int
}

func GetServices() ServicesStats {
	stats := ServicesStats{
		TransportTypes: map[string]int{},
		Datasets:       map[string]int{},
	}
	servicesCollection := database.GetCollection("services")

	numberServices, _ := servicesCollection.CountDocuments(context.Background(), bson.D{})
	stats.Total = int(numberServices)

	stats.TransportTypes = CountAggregate(servicesCollection, "$transporttype")
	stats.Datasets = CountAggregate(servicesCollection, "$datasource.datasetid")
	stats.Providers = CountAggregate(servicesCollection, "$datasource.providerid")
	stats.Countries = CountCountries(stats.Datasets)

	return stats
}
