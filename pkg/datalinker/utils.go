package datalinker

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func copyCollection(source string, destination string) {
	log.Info().Str("src", source).Str("dst", destination).Msg("Copying collection")
	sourceCollection := database.GetCollection(source)

	aggregation := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{}}},
		bson.D{{Key: "$out", Value: destination}},
	}

	sourceCollection.Aggregate(context.Background(), aggregation)
}

func emptyCollection(collectionName string) {
	log.Info().Str("collection", collectionName).Msg("Emptying collection")
	collection := database.GetCollection(collectionName)

	collection.DeleteMany(context.Background(), bson.M{})
}

func hasOverlap(arr1, arr2 []string) bool {
	// Create a map to store elements from the first array
	elementSet := map[string]struct{}{}
	for _, val := range arr1 {
		elementSet[val] = struct{}{}
	}

	// Check if any element of the second array is present in the map
	for _, val := range arr2 {
		if _, exists := elementSet[val]; exists {
			return true
		}
	}
	return false
}
