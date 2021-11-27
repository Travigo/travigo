package naptan

import (
	"context"
	"errors"

	"github.com/britbus/britbus/pkg/database"
	"github.com/kr/pretty"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx"
)

type NaPTAN struct {
	CreationDateTime     string `xml:",attr"`
	ModificationDateTime string `xml:",attr"`

	SchemaVersion string `xml:",attr"`

	StopPoints []StopPoint
	StopAreas  []StopArea
}

func (n *NaPTAN) Validate() error {
	if n.CreationDateTime == "" {
		return errors.New("CreationDateTime must be set")
	}
	if n.ModificationDateTime == "" {
		return errors.New("ModificationDateTime must be set")
	}
	if n.SchemaVersion != "2.4" {
		return errors.New("SchemaVersion must be 2.4")
	}

	return nil
}

func (naptanDoc *NaPTAN) ImportIntoMongo() {
	stopsCollection := database.GetCollection("stops")
	stopAreasCollection := database.GetCollection("stopareas")

	stopPointIndex := []mongo.IndexModel{
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
	_, err := stopsCollection.Indexes().CreateMany(context.Background(), stopPointIndex, opts)
	if err != nil {
		panic(err)
	}

	stopAreaIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "stopareacode", Value: bsonx.Int32(1)}},
		},
	}

	opts = options.CreateIndexes()
	_, err = stopAreasCollection.Indexes().CreateMany(context.Background(), stopAreaIndex, opts)
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

	// The same but for StopAreas
	for i := 0; i < len(naptanDoc.StopAreas); i++ {
		stopArea := naptanDoc.StopAreas[i]

		var existingStopArea *StopArea
		stopAreasCollection.FindOne(context.Background(), bson.M{"stopareacode": stopArea.StopAreaCode}).Decode(&existingStopArea)

		if existingStopArea == nil {
			bsonRep, _ := bson.Marshal(stopArea)
			_, err := stopAreasCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		}

		if existingStopArea.ModificationDateTime != stopArea.ModificationDateTime {
			bsonRep, _ := bson.Marshal(stopArea)
			_, err := stopAreasCollection.ReplaceOne(context.Background(), bson.M{"stopareacode": stopArea.StopAreaCode}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}
}
