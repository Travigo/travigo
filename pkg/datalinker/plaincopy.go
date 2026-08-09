package datalinker

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const recentlyUpdatedDatasetWindow = 24 * time.Hour

type PlainCopyConfig struct {
	SkipStaging bool
}

type PlainCopyLinker struct {
	objectName string
}

func NewPlainCopyLinker(objectName string) PlainCopyLinker {
	return PlainCopyLinker{objectName: objectName}
}

func (l PlainCopyLinker) collectionNames() (string, string, string) {
	liveCollectionName := fmt.Sprintf("%ss", l.objectName)
	return liveCollectionName, liveCollectionName + "_raw", liveCollectionName + "_staging"
}

func (l PlainCopyLinker) Run(config PlainCopyConfig) error {
	liveCollectionName, rawCollectionName, stagingCollectionName := l.collectionNames()

	if config.SkipStaging {
		if err := l.publishRecentlyUpdatedDatasets(context.Background(), rawCollectionName, liveCollectionName, time.Now().Add(-recentlyUpdatedDatasetWindow)); err != nil {
			return err
		}
	} else {
		dropCollection(stagingCollectionName)
		defer dropCollection(stagingCollectionName)

		if err := copyCollection(rawCollectionName, stagingCollectionName); err != nil {
			return err
		}
		if err := copyCollection(stagingCollectionName, liveCollectionName); err != nil {
			return err
		}
	}

	return datasetversion.Upsert(context.Background(), ctdf.DatasetVersion{
		Dataset:      datasetversion.LinkerDataset(l.objectName),
		LastModified: time.Now(),
	})
}

func (l PlainCopyLinker) publishRecentlyUpdatedDatasets(ctx context.Context, rawCollectionName string, liveCollectionName string, updatedAfter time.Time) error {
	datasetIDs, err := recentlyUpdatedDatasetIDs(ctx, updatedAfter)
	if err != nil {
		return err
	}
	if len(datasetIDs) == 0 {
		log.Info().Time("updated_after", updatedAfter).Msg("No recently updated datasets to publish")
		return nil
	}

	rawCollection := database.GetCollection(rawCollectionName)
	liveCollection := database.GetCollection(liveCollectionName)
	datasetFilter := bson.M{"datasource.datasetid": bson.M{"$in": datasetIDs}}

	timestampsByDataset, err := currentDatasetTimestamps(ctx, rawCollection, datasetFilter)
	if err != nil {
		return err
	}

	log.Info().
		Int("datasets", len(datasetIDs)).
		Time("updated_after", updatedAfter).
		Msg("Publishing recently updated datasets without staging")

	mergePipeline := incrementalMergePipeline(datasetFilter, liveCollectionName)
	cursor, err := rawCollection.Aggregate(ctx, mergePipeline)
	if err != nil {
		return fmt.Errorf("publish recently updated datasets to %s: %w", liveCollectionName, err)
	}
	if err := cursor.Close(ctx); err != nil {
		return fmt.Errorf("close recent dataset publisher cursor: %w", err)
	}
	if err := cursor.Err(); err != nil {
		return fmt.Errorf("publish recently updated datasets to %s: %w", liveCollectionName, err)
	}

	deleteModels := make([]mongo.WriteModel, 0, len(datasetIDs))
	for _, datasetID := range datasetIDs {
		filter := staleDatasetRecordsFilter(datasetID, timestampsByDataset[datasetID])
		deleteModels = append(deleteModels, mongo.NewDeleteManyModel().SetFilter(filter))
	}

	result, err := liveCollection.BulkWrite(ctx, deleteModels, options.BulkWrite().SetOrdered(false))
	if err != nil {
		return fmt.Errorf("delete stale records from %s: %w", liveCollectionName, err)
	}
	log.Info().
		Int("datasets", len(datasetIDs)).
		Int64("deleted", result.DeletedCount).
		Msg("Published recently updated datasets")

	return nil
}

func recentlyUpdatedDatasetIDs(ctx context.Context, updatedAfter time.Time) ([]string, error) {
	cursor, err := database.GetCollection("dataset_versions").Find(
		ctx,
		bson.M{"lastmodified": bson.M{"$gte": updatedAfter}},
		options.Find().SetProjection(bson.M{"dataset": 1}),
	)
	if err != nil {
		return nil, fmt.Errorf("find recently updated datasets: %w", err)
	}
	defer cursor.Close(ctx)

	datasetIDSet := map[string]struct{}{}
	for cursor.Next(ctx) {
		var version ctdf.DatasetVersion
		if err := cursor.Decode(&version); err != nil {
			return nil, fmt.Errorf("decode recently updated dataset: %w", err)
		}
		if version.Dataset != "" {
			datasetIDSet[version.Dataset] = struct{}{}
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate recently updated datasets: %w", err)
	}

	datasetIDs := make([]string, 0, len(datasetIDSet))
	for datasetID := range datasetIDSet {
		datasetIDs = append(datasetIDs, datasetID)
	}
	sort.Strings(datasetIDs)
	return datasetIDs, nil
}

func currentDatasetTimestamps(ctx context.Context, rawCollection *mongo.Collection, datasetFilter bson.M) (map[string][]string, error) {
	pipeline := mongo.Pipeline{
		bson.D{{Key: "$match", Value: datasetFilter}},
		bson.D{{Key: "$group", Value: bson.D{
			{Key: "_id", Value: "$datasource.datasetid"},
			{Key: "timestamps", Value: bson.D{{Key: "$addToSet", Value: "$datasource.timestamp"}}},
		}}},
	}
	cursor, err := rawCollection.Aggregate(ctx, pipeline)
	if err != nil {
		return nil, fmt.Errorf("load current raw dataset timestamps: %w", err)
	}
	defer cursor.Close(ctx)

	timestampsByDataset := map[string][]string{}
	for cursor.Next(ctx) {
		var result struct {
			DatasetID  string   `bson:"_id"`
			Timestamps []string `bson:"timestamps"`
		}
		if err := cursor.Decode(&result); err != nil {
			return nil, fmt.Errorf("decode current raw dataset timestamps: %w", err)
		}
		timestampsByDataset[result.DatasetID] = result.Timestamps
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate current raw dataset timestamps: %w", err)
	}

	return timestampsByDataset, nil
}

func staleDatasetRecordsFilter(datasetID string, currentTimestamps []string) bson.M {
	filter := bson.M{"datasource.datasetid": datasetID}
	if len(currentTimestamps) > 0 {
		filter["datasource.timestamp"] = bson.M{"$nin": currentTimestamps}
	}
	return filter
}

func incrementalMergePipeline(datasetFilter bson.M, liveCollectionName string) mongo.Pipeline {
	return mongo.Pipeline{
		bson.D{{Key: "$match", Value: datasetFilter}},
		bson.D{{Key: "$merge", Value: bson.D{
			{Key: "into", Value: liveCollectionName},
			{Key: "on", Value: "_id"},
			{Key: "whenMatched", Value: "replace"},
			{Key: "whenNotMatched", Value: "insert"},
		}}},
	}
}
