package datalinker

import (
	"context"
	"fmt"
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

type StopsLinker struct {
}

func NewStopsLinker() StopsLinker {
	return StopsLinker{}
}

func (l StopsLinker) GetBaseCollectionName() string {
	return "stops"
}

func (l StopsLinker) Run() {
	liveCollectionName := l.GetBaseCollectionName()
	rawCollectionName := fmt.Sprintf("%s_raw", liveCollectionName)
	stagingCollectionName := fmt.Sprintf("%s_staging", liveCollectionName)

	rawCollection := database.GetCollection(rawCollectionName)

	copyCollection(rawCollectionName, stagingCollectionName)

	// Get matching records
	aggregation := mongo.Pipeline{
		bson.D{{Key: "$addFields", Value: bson.D{{Key: "otheridentifier", Value: "$otheridentifiers"}}}},
		bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$otheridentifier"}}}},
		bson.D{
			{
				Key: "$match",
				Value: bson.M{
					"$or": bson.A{
						// Identical values of these we will be merging by
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

	cursor, err := rawCollection.Aggregate(context.Background(), aggregation)
	if err != nil {
		panic(err)
	}

	var operations []mongo.WriteModel
	var mergeGroups [][]string

	for cursor.Next(context.Background()) {
		var aggregatedRecords struct {
			ID      string `bson:"_id"`
			Count   int
			Records []BaseRecord
		}
		err := cursor.Decode(&aggregatedRecords)
		if err != nil {
			log.Error().Err(err).Msg("Failed to decode aggregatedRecords")
		}

		var identifiers []string

		for _, record := range aggregatedRecords.Records {
			identifiers = append(identifiers, record.PrimaryIdentifier)
			identifiers = append(identifiers, record.OtherIdentifiers...)
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

		// Get the primary records so we can do the actual merging with them
		var primaryRecords []ctdf.Stop
		for _, id := range mergeGroupFiltered {
			var record ctdf.Stop

			cursor := rawCollection.FindOne(context.Background(), bson.M{"primaryidentifier": id})
			err := cursor.Decode(&record)
			if err == nil {
				primaryRecords = append(primaryRecords, record)
			}
		}

		if len(primaryRecords) == 0 {
			continue
		}

		// Now delete all the records that are being filtered
		for _, id := range mergeGroup {
			deleteModel := mongo.NewDeleteOneModel()
			deleteModel.SetFilter(bson.M{"primaryidentifier": id})
			operations = append(operations, deleteModel)
		}

		// Sort them by creation date and for now we'll just base it on the oldest record
		// TODO some actual deep merging
		sort.SliceStable(primaryRecords, func(i, j int) bool {
			return primaryRecords[i].CreationDateTime.Before(primaryRecords[j].CreationDateTime)
		})

		// Create new record
		newRecord := primaryRecords[0]
		newRecord.PrimaryIdentifier = uuid.New().String()
		newRecord.OtherIdentifiers = mergeGroupFiltered

		// insert new
		insertModel := mongo.NewInsertOneModel()
		bsonRep, _ := bson.Marshal(newRecord)
		insertModel.SetDocument(bsonRep)
		operations = append(operations, insertModel)

		pretty.Println(mergeGroupFiltered)
		pretty.Println(len(primaryRecords))
	}

	if len(operations) > 0 {
		stagingCollection := database.GetCollection(stagingCollectionName)

		_, err := stagingCollection.BulkWrite(context.Background(), operations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write")
		}
	}

	// Copy staging to live
	copyCollection(stagingCollectionName, liveCollectionName)
	// Delete staging as it's not needed now
	emptyCollection(stagingCollectionName)
}
