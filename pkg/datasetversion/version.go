package datasetversion

import (
	"context"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const StopTransfersDataset = "stop-transfers"

func LinkerDataset(objectName string) string {
	return "linker-" + objectName + "s"
}

// Upsert records that a dataset-producing operation completed successfully.
func Upsert(ctx context.Context, version ctdf.DatasetVersion) error {
	_, err := database.GetCollection("dataset_versions").UpdateOne(
		ctx,
		bson.M{"dataset": version.Dataset},
		bson.M{"$set": version},
		options.Update().SetUpsert(true),
	)
	return err
}

// NeedsRefresh reports whether any source dataset has changed since the
// derived dataset was last completed.
func NeedsRefresh(ctx context.Context, derivedDataset string, sourceDatasets []string) (bool, error) {
	changed, err := ChangedDatasets(ctx, derivedDataset, sourceDatasets)
	return len(changed) > 0, err
}

func ChangedDatasets(ctx context.Context, derivedDataset string, sourceDatasets []string) ([]string, error) {
	var derivedVersion ctdf.DatasetVersion
	err := database.GetCollection("dataset_versions").FindOne(ctx, bson.M{
		"dataset": derivedDataset,
	}).Decode(&derivedVersion)
	if err == mongo.ErrNoDocuments {
		return sourceDatasets, nil
	}
	if err != nil {
		return nil, err
	}
	if len(sourceDatasets) == 0 {
		return nil, nil
	}

	// Read source versions together, then walk the caller's slice so the result
	// keeps its ordering (and any duplicate source IDs).
	cursor, err := database.GetCollection("dataset_versions").Find(ctx, bson.M{
		"dataset":      bson.M{"$in": sourceDatasets},
		"lastmodified": bson.M{"$gt": derivedVersion.LastModified},
	}, options.Find().SetProjection(bson.M{"_id": 0, "dataset": 1, "lastmodified": 1}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	newer := make(map[string]bool)
	for cursor.Next(ctx) {
		var sourceVersion ctdf.DatasetVersion
		if err := cursor.Decode(&sourceVersion); err != nil {
			return nil, err
		}
		if sourceVersion.LastModified.After(derivedVersion.LastModified) {
			newer[sourceVersion.Dataset] = true
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	changed := make([]string, 0, len(sourceDatasets))
	for _, sourceDataset := range sourceDatasets {
		if newer[sourceDataset] {
			changed = append(changed, sourceDataset)
		}
	}
	return changed, nil
}
