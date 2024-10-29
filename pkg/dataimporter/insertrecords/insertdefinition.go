package insertrecords

import (
	"context"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type InsertDefinition struct {
	Collection string                 `yaml:"Collection"`
	Match      map[string]string      `yaml:"Match"`
	Data       map[string]interface{} `yaml:"Data"`
}

func (i *InsertDefinition) Upsert() {
	collection := database.GetCollection(i.Collection)

	query, err := bson.Marshal(i.Match)
	if err != nil {
		log.Error().Err(err).Msg("Insert definition match marshall")
		return
	}

	i.Data["datasource"] = map[string]string{
		"originalformat": "travigo-yaml",
		"provider":       "Travigo",
		"datasetid":      "travigo-insert-record",
		"timestamp":      "9999999999",
	}

	opts := options.Update().SetUpsert(true)
	_, err = collection.UpdateOne(context.Background(), query, bson.M{"$set": i.Data}, opts)
	if err != nil {
		log.Error().Err(err).Msg("Insert definition update")
		return
	}
}
