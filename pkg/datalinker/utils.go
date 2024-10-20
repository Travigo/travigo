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
