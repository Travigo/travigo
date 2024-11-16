package calculator

import (
	"context"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

type OperatorsStats struct {
	Total int

	Datasets  map[string]int
	Providers map[string]int
	Countries map[string]int
}

func GetOperators() OperatorsStats {
	stats := OperatorsStats{}
	operatorsCollection := database.GetCollection("operators")
	numberoperators, _ := operatorsCollection.CountDocuments(context.Background(), bson.D{})
	stats.Total = int(numberoperators)

	stats.Datasets = CountAggregate(operatorsCollection, "$datasource.datasetid")
	stats.Providers = CountAggregate(operatorsCollection, "$datasource.providerid")
	stats.Countries = CountCountries(stats.Datasets)

	return stats
}
