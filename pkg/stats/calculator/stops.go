package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type StopsStats struct {
	Total int

	Datasets  map[string]int
	Providers map[string]int
	Countries map[string]int
}

func GetStops() StopsStats {
	stats := StopsStats{}
	stopsCollection := database.GetCollection("stops")
	numberStops, _ := stopsCollection.CountDocuments(context.Background(), bson.D{})
	stats.Total = int(numberStops)

	stats.Datasets = CountAggregate(stopsCollection, "$datasource.datasetid")
	stats.Providers = CountAggregate(stopsCollection, "$datasource.providerid")
	stats.Countries = CountCountries(stats.Datasets)

	return stats
}
