package datalinker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type OldLinker interface {
	GetBaseCollectionName() string
	Run()
}

type Linker[T ctdf.LinkableRecord] struct {
	objectName  string
	aggregation mongo.Pipeline
}

func NewLinker[T ctdf.LinkableRecord](objectName string, aggregation mongo.Pipeline) Linker[T] {
	return Linker[T]{
		objectName:  objectName,
		aggregation: aggregation,
	}
}
func (l Linker[T]) GetBaseCollectionName() string {
	return fmt.Sprintf("%ss", l.objectName)
}

func (l Linker[T]) Run() {
	liveCollectionName := l.GetBaseCollectionName()
	rawCollectionName := fmt.Sprintf("%s_raw", liveCollectionName)
	stagingCollectionName := fmt.Sprintf("%s_staging", liveCollectionName)

	rawCollection := database.GetCollection(rawCollectionName)
	stagingCollection := database.GetCollection(stagingCollectionName)

	copyCollection(rawCollectionName, stagingCollectionName)

	// Get matching records
	cursor, err := rawCollection.Aggregate(context.Background(), l.aggregation)
	if err != nil {
		panic(err)
	}

	var operations []mongo.WriteModel
	var mergeGroups [][]string

	for cursor.Next(context.Background()) {
		var aggregatedRecords struct {
			ID      string `bson:"_id"`
			Count   int
			Records []ctdf.BaseRecord
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
			if util.SlicesOverlap(mergeGroups[i], mergeGroups[j]) {
				log.Debug().Msgf("Array %d and Array %d have overlapping values\n", i, j)

				mergeGroups[i] = util.RemoveDuplicateStrings(append(mergeGroups[i], mergeGroups[j]...), []string{})
				mergeGroups[j] = []string{}
			}
		}
	}

	manualMergeRegex := regexp.MustCompile("travigo-internalmerge-")
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
		var primaryRecords []T
		for _, id := range mergeGroupFiltered {
			var record T

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
			return primaryRecords[i].GetCreationDateTime().Before(primaryRecords[j].GetCreationDateTime())
		})

		// Create new record
		newRecord := primaryRecords[0]

		// Generate an ID for the record
		idHasher := sha256.New()
		newRecord.GenerateDeterministicID(idHasher)

		idHash := fmt.Sprintf("%x", idHasher.Sum(nil))[:28]
		newRecord.SetPrimaryIdentifier(fmt.Sprintf("tmr-%s-%s", l.objectName, idHash))

		newRecord.SetOtherIdentifiers(append(mergeGroupFiltered, newRecord.GetPrimaryIdentifier()))

		// insert new
		insertModel := mongo.NewInsertOneModel()
		bsonRep, _ := bson.Marshal(newRecord)
		insertModel.SetDocument(bsonRep)
		operations = append(operations, insertModel)

		pretty.Println(mergeGroupFiltered)
		pretty.Println(len(primaryRecords))
	}

	if len(operations) > 0 {
		_, err := stagingCollection.BulkWrite(context.Background(), operations, &options.BulkWriteOptions{})
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to bulk write")
		}
	}

	// Delete any remaining manual merge entries from staging
	stagingCollection.DeleteMany(context.Background(), bson.M{"primaryidentifier": bson.M{"$regex": "^travigo-internalmerge-"}})

	// Copy staging to live
	copyCollection(stagingCollectionName, liveCollectionName)
	// Delete staging as it's not needed now
	emptyCollection(stagingCollectionName)
}
