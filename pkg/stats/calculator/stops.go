package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type StopsStats struct {
	Total int

	Datasources map[string]int
	Countries   map[string]int
}

func GetStops() StopsStats {
	stats := StopsStats{}
	stopsCollection := database.GetCollection("stops")
	numberStops, _ := stopsCollection.CountDocuments(context.Background(), bson.D{})
	stats.Total = int(numberStops)

	stats.Datasources = CountAggregate(stopsCollection, "$datasource.datasetid")
	stats.Countries = CountCountries(stats.Datasources)

	return stats
}
