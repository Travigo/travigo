package calculator

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func CountAggregate(collection *mongo.Collection, aggregateKey string) map[string]int {
	countMap := map[string]int{}

	aggregation := mongo.Pipeline{
		bson.D{
			{Key: "$group",
				Value: bson.D{
					{Key: "_id", Value: aggregateKey},
					{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
				},
			},
		},
	}
	var result []bson.M
	cursor, _ := collection.Aggregate(context.Background(), aggregation)
	cursor.All(context.Background(), &result)

	for _, record := range result {
		countMap[record["_id"].(string)] = int(record["count"].(int32))
	}

	return countMap
}

func CountCountries(datasources map[string]int) map[string]int {
	countries := map[string]int{}

	for datasource, count := range datasources {
		// Making a big assumption here
		datasourceSplit := strings.Split(datasource, "-")
		country := datasourceSplit[0]

		countries[country] = count
	}

	return countries
}
