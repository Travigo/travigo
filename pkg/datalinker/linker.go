package datalinker

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type OldLinker interface {
	GetBaseCollectionName() string
	Run() error
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

func (l Linker[T]) Run() error {
	liveCollectionName := l.GetBaseCollectionName()
	rawCollectionName := fmt.Sprintf("%s_raw", liveCollectionName)
	stagingCollectionName := fmt.Sprintf("%s_staging", liveCollectionName)

	rawCollection := database.GetCollection(rawCollectionName)
	stagingCollection := database.GetCollection(stagingCollectionName)

	dropCollection(stagingCollectionName)
	defer dropCollection(stagingCollectionName)

	copyCollection(rawCollectionName, stagingCollectionName)

	log.Info().Msg("Fetching aggregate records started")

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

	log.Info().Msg("Fetching aggregate records finished")

	log.Info().Msg("Data linking started")
	mergeGroups = mergeOverlappingIdentifierGroups(mergeGroups)
	primaryRecordsByIdentifier, err := loadLinkerPrimaryRecords[T](context.Background(), rawCollection, mergeGroups)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to batch load linker records")
	}

	for _, mergeGroup := range mergeGroups {
		if len(mergeGroup) == 0 {
			continue
		}

		// Filter for manual ones
		var mergeGroupFiltered []string
		for _, id := range mergeGroup {
			if !strings.Contains(id, "travigo-internalmerge-") {
				mergeGroupFiltered = append(mergeGroupFiltered, id)
			}
		}

		// Get the primary records so we can do the actual merging with them.
		// They were loaded in batches before this loop to avoid one MongoDB read per ID.
		var primaryRecords []T
		for _, id := range mergeGroupFiltered {
			if record, ok := primaryRecordsByIdentifier[id]; ok {
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
			iTime := primaryRecords[i].GetCreationDateTime()
			jTime := primaryRecords[j].GetCreationDateTime()
			return (iTime != time.Date(1, time.January, 1, 0, 0, 0, 0, time.UTC)) && iTime.Before(jTime)
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

	}

	log.Info().Msg("Data linking finished")

	log.Info().Msg("Writing linked records")

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

	compactLinkedCollections(context.Background(), rawCollectionName, liveCollectionName)

	return datasetversion.Upsert(context.Background(), ctdf.DatasetVersion{
		Dataset:      datasetversion.LinkerDataset(l.objectName),
		LastModified: time.Now(),
	})
}

type identifierMergeGroup struct {
	identifiers []string
	seen        map[string]struct{}
}

func mergeOverlappingIdentifierGroups(groups [][]string) [][]string {
	mergedGroups := make([]identifierMergeGroup, 0, len(groups))
	groupByIdentifier := map[string]int{}

	for _, group := range groups {
		matchingGroups := map[int]struct{}{}
		for _, identifier := range group {
			if groupIndex, ok := groupByIdentifier[identifier]; ok {
				matchingGroups[groupIndex] = struct{}{}
			}
		}

		baseIndex := -1
		for groupIndex := range matchingGroups {
			if baseIndex == -1 || groupIndex < baseIndex {
				baseIndex = groupIndex
			}
		}
		if baseIndex == -1 {
			baseIndex = len(mergedGroups)
			mergedGroups = append(mergedGroups, identifierMergeGroup{seen: map[string]struct{}{}})
		}

		for groupIndex := range matchingGroups {
			if groupIndex == baseIndex || len(mergedGroups[groupIndex].identifiers) == 0 {
				continue
			}
			for _, identifier := range mergedGroups[groupIndex].identifiers {
				if _, exists := mergedGroups[baseIndex].seen[identifier]; !exists {
					mergedGroups[baseIndex].seen[identifier] = struct{}{}
					mergedGroups[baseIndex].identifiers = append(mergedGroups[baseIndex].identifiers, identifier)
				}
				groupByIdentifier[identifier] = baseIndex
			}
			mergedGroups[groupIndex].identifiers = nil
		}

		for _, identifier := range group {
			if _, exists := mergedGroups[baseIndex].seen[identifier]; !exists {
				mergedGroups[baseIndex].seen[identifier] = struct{}{}
				mergedGroups[baseIndex].identifiers = append(mergedGroups[baseIndex].identifiers, identifier)
			}
			groupByIdentifier[identifier] = baseIndex
		}
	}

	result := make([][]string, 0, len(mergedGroups))
	for _, group := range mergedGroups {
		if len(group.identifiers) > 0 {
			result = append(result, group.identifiers)
		}
	}
	return result
}

func loadLinkerPrimaryRecords[T ctdf.LinkableRecord](ctx context.Context, collection *mongo.Collection, groups [][]string) (map[string]T, error) {
	identifiers := []string{}
	seenIdentifiers := map[string]struct{}{}
	for _, group := range groups {
		for _, identifier := range group {
			if strings.Contains(identifier, "travigo-internalmerge-") {
				continue
			}
			if _, seen := seenIdentifiers[identifier]; seen {
				continue
			}
			seenIdentifiers[identifier] = struct{}{}
			identifiers = append(identifiers, identifier)
		}
	}

	recordsByIdentifier := make(map[string]T, len(identifiers))
	const batchSize = 1000
	for start := 0; start < len(identifiers); start += batchSize {
		end := start + batchSize
		if end > len(identifiers) {
			end = len(identifiers)
		}

		cursor, err := collection.Find(ctx, bson.M{"primaryidentifier": bson.M{"$in": identifiers[start:end]}})
		if err != nil {
			return nil, err
		}
		for cursor.Next(ctx) {
			var record T
			if err := cursor.Decode(&record); err != nil {
				cursor.Close(ctx)
				return nil, err
			}
			recordsByIdentifier[record.GetPrimaryIdentifier()] = record
		}
		if err := cursor.Err(); err != nil {
			cursor.Close(ctx)
			return nil, err
		}
		cursor.Close(ctx)
	}

	return recordsByIdentifier, nil
}
