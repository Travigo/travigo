package naptan

import (
	"context"
	"errors"

	"github.com/britbus/britbus/pkg/ctdf"
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

func (naptanDoc *NaPTAN) ImportIntoMongoAsCTDF() {
	stopsCollection := database.GetCollection("stops")
	stopAreasCollection := database.GetCollection("stop_groups")

	stopsIndex := []mongo.IndexModel{
		{
			Keys: bsonx.Doc{{Key: "primaryidentifier", Value: bsonx.Int32(1)}},
		},
		{
			Keys: bsonx.Doc{{Key: "location", Value: bsonx.String("2dsphere")}},
		},
	}

	opts := options.CreateIndexes()
	_, err := stopsCollection.Indexes().CreateMany(context.Background(), stopsIndex, opts)
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
		naptanStopPoint := naptanDoc.StopPoints[i]
		ctdfStop := naptanStopPoint.ToCTDF()

		var existingCtdfStop *ctdf.Stop
		stopsCollection.FindOne(context.Background(), bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier}).Decode(&existingCtdfStop)

		if existingCtdfStop == nil {
			bsonRep, _ := bson.Marshal(ctdfStop)
			_, err := stopsCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		}

		if existingCtdfStop.ModificationDateTime != ctdfStop.ModificationDateTime {
			bsonRep, _ := bson.Marshal(ctdfStop)
			_, err := stopsCollection.ReplaceOne(context.Background(), bson.M{"primaryidentifier": ctdfStop.PrimaryIdentifier}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}

	// The same but for StopAreas
	for i := 0; i < len(naptanDoc.StopAreas); i++ {
		naptanStopArea := naptanDoc.StopAreas[i]
		ctdfStopGroup := naptanStopArea.ToCTDF()

		var existingStopGroup *ctdf.StopGroup
		stopAreasCollection.FindOne(context.Background(), bson.M{"identifier": ctdfStopGroup.Identifier}).Decode(&existingStopGroup)

		if existingStopGroup == nil {
			bsonRep, _ := bson.Marshal(ctdfStopGroup)
			_, err := stopAreasCollection.InsertOne(context.Background(), bsonRep)

			if err != nil {
				pretty.Println(err)
			}

			continue
		}

		if existingStopGroup.ModificationDateTime != ctdfStopGroup.ModificationDateTime {
			bsonRep, _ := bson.Marshal(ctdfStopGroup)
			_, err := stopAreasCollection.ReplaceOne(context.Background(), bson.M{"identifier": ctdfStopGroup.Identifier}, bsonRep)

			if err != nil {
				pretty.Println(err)
			}
		}
	}
}
