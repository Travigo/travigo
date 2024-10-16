package datalinker

import (
	"context"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

func StopExample(collectionName string) {
	collection := database.GetCollection(collectionName)

	aggregation := mongo.Pipeline{
		bson.D{{"$addFields", bson.D{{"otheridentifier", "$otheridentifiers"}}}},
		bson.D{{"$unwind", bson.D{{"path", "$otheridentifier"}}}},
		bson.D{{"$match", bson.D{{"otheridentifier", bson.D{{"$regex", "^GB:CRS:"}}}}}},
		bson.D{
			{"$group",
				bson.D{
					{"_id", "$otheridentifier"},
					{"count", bson.D{{"$sum", 1}}},
					{"records", bson.D{{"$push", "$$ROOT"}}},
				},
			},
		},
		bson.D{{"$sort", bson.D{{"count", -1}}}},
		bson.D{{"$match", bson.D{{"count", bson.D{{"$gt", 1}}}}}},
	}

	cursor, err := collection.Aggregate(context.TODO(), aggregation)
	if err != nil {
		panic(err)
	}

	var operations []mongo.WriteModel

	for cursor.Next(context.Background()) {
		var aggregatedRecords struct {
			ID      string `bson:"_id"`
			Count   int
			Records []ctdf.Stop
		}
		err := cursor.Decode(&aggregatedRecords)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode aggregatedRecords")
		}

		pretty.Println(aggregatedRecords.ID)

		var identifiers []string

		for _, record := range aggregatedRecords.Records {
			identifiers = append(identifiers, record.PrimaryIdentifier)
			identifiers = append(identifiers, record.OtherIdentifiers...)

			//Delete old
			deleteModel := mongo.NewDeleteOneModel()

			deleteModel.SetFilter(bson.M{"primaryidentifier": record.PrimaryIdentifier})

			operations = append(operations, deleteModel)
		}

		identifiers = util.RemoveDuplicateStrings(identifiers, []string{})
		pretty.Println(identifiers)

		newRecord := aggregatedRecords.Records[0]

		newRecord.PrimaryIdentifier = aggregatedRecords.ID
		newRecord.OtherIdentifiers = identifiers

		// insert ew
		insertModel := mongo.NewInsertOneModel()

		bsonRep, _ := bson.Marshal(newRecord)
		insertModel.SetDocument(bsonRep)

		operations = append(operations, insertModel)
	}

	if len(operations) > 0 {
		_, err := collection.BulkWrite(context.Background(), operations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write")
		}
	}
}
