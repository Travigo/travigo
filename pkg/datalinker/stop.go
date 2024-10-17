package datalinker

import (
	"context"
	"regexp"
	"sort"

	"github.com/google/uuid"
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
		bson.D{{Key: "$addFields", Value: bson.D{{Key: "otheridentifier", Value: "$otheridentifiers"}}}},
		bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$otheridentifier"}}}},
		bson.D{
			{
				Key: "$match",
				Value: bson.M{
					"$or": bson.A{
						bson.M{"otheridentifier": bson.M{"$regex": "^GB:CRS:"}},
						bson.M{"otheridentifier": bson.M{"$regex": "^TRAVIGO:MANUALMERGE:"}},
					},
				},
			},
		},
		bson.D{
			{Key: "$group",
				Value: bson.D{
					{Key: "_id", Value: "$otheridentifier"},
					{Key: "count", Value: bson.D{{Key: "$sum", Value: 1}}},
					{Key: "records", Value: bson.D{{Key: "$push", Value: "$$ROOT"}}},
				},
			},
		},
		bson.D{{Key: "$sort", Value: bson.D{{Key: "count", Value: -1}}}},
		bson.D{{Key: "$match", Value: bson.D{{Key: "count", Value: bson.D{{Key: "$gt", Value: 1}}}}}},
	}

	cursor, err := collection.Aggregate(context.TODO(), aggregation)
	if err != nil {
		panic(err)
	}

	var operations []mongo.WriteModel
	var mergeGroups [][]string

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

		var identifiers []string

		for _, record := range aggregatedRecords.Records {
			identifiers = append(identifiers, record.PrimaryIdentifier)
			identifiers = append(identifiers, record.OtherIdentifiers...)

			//Delete old
			// deleteModel := mongo.NewDeleteOneModel()
			// deleteModel.SetFilter(bson.M{"primaryidentifier": record.PrimaryIdentifier})
			// operations = append(operations, deleteModel)
		}

		identifiers = util.RemoveDuplicateStrings(identifiers, []string{})

		mergeGroups = append(mergeGroups, identifiers)
	}

	for i := 0; i < len(mergeGroups); i++ {
		for j := i + 1; j < len(mergeGroups); j++ {
			if hasOverlap(mergeGroups[i], mergeGroups[j]) {
				log.Debug().Msgf("Array %d and Array %d have overlapping values\n", i, j)

				mergeGroups[i] = util.RemoveDuplicateStrings(append(mergeGroups[i], mergeGroups[j]...), []string{})
				mergeGroups[j] = []string{}
			}
		}
	}

	manualMergeRegex := regexp.MustCompile("TRAVIGO:MANUALMERGE:")
	for _, mergeGroup := range mergeGroups {
		if len(mergeGroup) == 0 {
			continue
		}

		// Filter for manual ones
		var mergeGroupFiltered []string
		for _, id := range mergeGroup {
			if !manualMergeRegex.MatchString(id) {
				mergeGroupFiltered = append(mergeGroupFiltered, id)
			}
		}
		mergeGroup = mergeGroupFiltered

		// Get the primary records so we can do the actual merging with them
		var primaryRecords []ctdf.Stop
		for _, id := range mergeGroup {
			var record ctdf.Stop

			cursor := collection.FindOne(context.Background(), bson.M{"primaryidentifier": id})
			err := cursor.Decode(&record)
			if err == nil {
				primaryRecords = append(primaryRecords, record)
			}
		}

		// Sort them by creation date and for now we'll just base it on the oldest record
		// TODO some actual deep merging
		sort.SliceStable(primaryRecords, func(i, j int) bool {
			return primaryRecords[i].CreationDateTime.Before(primaryRecords[j].CreationDateTime)
		})

		// Create new record
		newRecord := primaryRecords[0]
		newRecord.PrimaryIdentifier = uuid.New().String()
		newRecord.OtherIdentifiers = mergeGroup

		// insert new
		insertModel := mongo.NewInsertOneModel()
		bsonRep, _ := bson.Marshal(newRecord)
		insertModel.SetDocument(bsonRep)
		operations = append(operations, insertModel)

		pretty.Println(mergeGroup)
		pretty.Println(len(primaryRecords))
	}

	if len(operations) > 0 {
		collection = database.GetCollection("stops_linktest") // TODO DEBUG

		_, err := collection.BulkWrite(context.Background(), operations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write")
		}
	}
}

func hasOverlap(arr1, arr2 []string) bool {
	// Create a map to store elements from the first array
	elementSet := make(map[string]struct{})
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
