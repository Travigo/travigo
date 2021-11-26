package naptan

import (
	"context"
	"log"
	"time"

	"github.com/kr/pretty"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

func ImportNaPTANIntoMongo(naptanDoc *NaPTAN) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI("mongodb://localhost:27017"))
	if err != nil {
		log.Println(err)
	}

	stopsCollection := client.Database("britbus").Collection("stops")

	index := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "naptancode", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "atcocode", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "location.position", Value: bsonx.String("2dsphere")}},
		},
	}

	opts := options.CreateIndexes()
	_, err = stopsCollection.Indexes().CreateMany(ctx, index, opts)
	if err != nil {
		panic(err)
	}

	// Not the most optimised code created but it works
	for i := 0; i < len(naptanDoc.StopPoints); i++ {
		stopPoint := naptanDoc.StopPoints[i]

		var existingStopPoint *StopPoint
		stopsCollection.FindOne(context.Background(), bson.M{"naptancode": stopPoint.NaptanCode}).Decode(&existingStopPoint)

		if existingStopPoint == nil {
			bsonRep, _ := bson.Marshal(stopPoint)
			_, err := stopsCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		}

		if existingStopPoint.ModificationDateTime != stopPoint.ModificationDateTime {
			bsonRep, _ := bson.Marshal(stopPoint)
			_, err := stopsCollection.ReplaceOne(context.Background(), bson.M{"naptancode": stopPoint.NaptanCode}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}
}
