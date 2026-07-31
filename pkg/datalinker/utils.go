package datalinker

import (
	"context"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func copyCollection(source string, destination string) error {
	log.Info().Str("src", source).Str("dst", destination).Msg("Copying collection")
	sourceCollection := database.GetCollection(source)

	aggregation := mongo.Pipeline{
		bson.D{{Key: "$match", Value: bson.M{}}},
		bson.D{{Key: "$out", Value: destination}},
	}

	cursor, err := sourceCollection.Aggregate(context.Background(), aggregation)
	if err != nil {
		return fmt.Errorf("copy collection %s to %s: %w", source, destination, err)
	}
	defer cursor.Close(context.Background())

	return cursor.Err()
}

func dropCollection(collectionName string) {
	log.Info().Str("collection", collectionName).Msg("Dropping collection")
	collection := database.GetCollection(collectionName)

	if err := collection.Drop(context.Background()); err != nil {
		log.Error().Err(err).Str("collection", collectionName).Msg("Failed to drop collection")
	}
}
