package calculator

import (
	"context"

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
