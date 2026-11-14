package datalinker

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type PlainCopyConfig struct {
	SkipStaging bool
}

const sourceWriteFinishTimeout = 10 * time.Second

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

	ctx := context.Background()
	journeys := l.objectName == "journey"
	if journeys {
		if err := database.EnsureJourneyCollection(ctx, liveCollectionName); err != nil {
			return err
		}
	}
	linkerDataset := datasetversion.LinkerDataset(l.objectName)
	if config.SkipStaging {
		if err := publishUpdatedDatasets(ctx, rawCollectionName, liveCollectionName, linkerDataset); err != nil {
			return err
		}
	} else {
		dropCollection(stagingCollectionName)
		defer dropCollection(stagingCollectionName)
		if journeys {
			if err := database.EnsureJourneyCollection(ctx, stagingCollectionName); err != nil {
				return err
			}
		}

		if err := copyCollection(rawCollectionName, stagingCollectionName); err != nil {
			return err
		}
		if err := runJourneySourceWrite(ctx, func() error {
			if err := copyCollection(stagingCollectionName, liveCollectionName); err != nil {
				return err
			}
			if journeys {
				return database.EnsureJourneyCollection(ctx, liveCollectionName)
			}
			return nil
		}); err != nil {
			return err
		}
	}

	return datasetversion.Upsert(ctx, ctdf.DatasetVersion{
		Dataset:      linkerDataset,
		LastModified: time.Now(),
	})
}

// runJourneySourceWrite brackets changes to the published journey collection
// with a durable marker consumed by the stop-services builder. It is cheap on
// the publisher path and lets builders detect overlapping source changes
// without holding a lock through their full aggregation.
func runJourneySourceWrite(ctx context.Context, publish func() error, datasetIDs ...string) error {
	if database.Instance == nil || database.Instance.Database == nil {
		return errors.New("begin stop-services source write: database is not connected")
	}

	finish, err := stopservices.BeginSourceWrite(ctx, database.Instance.Database, datasetIDs...)
	if err != nil {
		return fmt.Errorf("begin stop-services source write: %w", err)
	}

	publishErr := publish()
	finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sourceWriteFinishTimeout)
	defer cancel()
	finishErr := finish(finishCtx)
	if publishErr != nil && finishErr != nil {
		return errors.Join(publishErr, fmt.Errorf("finish stop-services source write: %w", finishErr))
	}
	if publishErr != nil {
		return publishErr
	}
	if finishErr != nil {
		return fmt.Errorf("finish stop-services source write: %w", finishErr)
	}
	return nil
}

func publishUpdatedDatasets(ctx context.Context, rawCollectionName, liveCollectionName, linkerDataset string) error {
	versions := database.GetCollection("dataset_versions")
	pending, err := pendingJourneyDatasets(ctx, versions, linkerDataset)
	if err != nil {
		return err
	}

	rawCollection := database.GetCollection(rawCollectionName)
	liveCollection := database.GetCollection(liveCollectionName)
	log.Info().Int("datasets", len(pending)).Msg("Publishing changed journey datasets without staging")
	for _, version := range pending {
		log.Info().Str("dataset", version.Dataset).Time("version", version.LastModified).Msg("Publishing journey dataset")
		deleted, err := publishJourneyDataset(ctx, rawCollection, liveCollection, version.Dataset)
		if err != nil {
			return fmt.Errorf("publish journey dataset %s: %w", version.Dataset, err)
		}

		// Save the observed source version, so an import that finished during
		// publication is still picked up on the next run.
		result, err := versions.UpdateMany(ctx, bson.M{"dataset": version.Dataset}, bson.M{
			"$max": bson.M{"publishedjourneysversion": version.LastModified},
		})
		if err != nil {
			return fmt.Errorf("checkpoint journey dataset %s: %w", version.Dataset, err)
		}
		if result.MatchedCount == 0 {
			return fmt.Errorf("checkpoint journey dataset %s: source version disappeared", version.Dataset)
		}
		log.Info().Str("dataset", version.Dataset).Int64("deleted", deleted).Msg("Published journey dataset")
	}
	return nil
}

func pendingJourneyDatasets(ctx context.Context, versions *mongo.Collection, linkerDataset string) ([]ctdf.DatasetVersion, error) {
	cursor, err := versions.Find(ctx, bson.M{"dataset": bson.M{"$nin": []string{"", linkerDataset}}},
		options.Find().SetProjection(bson.M{
			"dataset": 1, "lastmodified": 1, "rawjourneysmodified": 1, "publishedjourneysversion": 1,
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("find journey dataset versions: %w", err)
	}
	defer cursor.Close(ctx)

	pendingByDataset := map[string]ctdf.DatasetVersion{}
	for cursor.Next(ctx) {
		var version ctdf.DatasetVersion
		if err := cursor.Decode(&version); err != nil {
			return nil, fmt.Errorf("decode journey dataset version: %w", err)
		}
		if version.RawJourneysModified.After(version.LastModified) {
			version.LastModified = version.RawJourneysModified
		}
		if version.Dataset == "" || !version.LastModified.After(version.PublishedJourneysVersion) {
			continue
		}
		if version.LastModified.After(pendingByDataset[version.Dataset].LastModified) {
			pendingByDataset[version.Dataset] = version
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("iterate journey dataset versions: %w", err)
	}

	pending := make([]ctdf.DatasetVersion, 0, len(pendingByDataset))
	for _, version := range pendingByDataset {
		pending = append(pending, version)
	}
	sort.Slice(pending, func(i, j int) bool { return pending[i].Dataset < pending[j].Dataset })
	return pending, nil
}

func publishJourneyDataset(ctx context.Context, rawCollection, liveCollection *mongo.Collection, datasetID string) (int64, error) {
	var deleted int64
	err := runJourneySourceWrite(ctx, func() error {
		datasetFilter := bson.M{"datasource.datasetid": datasetID}
		currentTimestamps, err := currentDatasetTimestamps(ctx, rawCollection, datasetFilter)
		if err != nil {
			return err
		}

		cursor, err := rawCollection.Aggregate(ctx, incrementalMergePipeline(datasetFilter, liveCollection.Name()))
		if err != nil {
			return fmt.Errorf("merge journeys: %w", err)
		}
		if err := cursor.Close(ctx); err != nil {
			return fmt.Errorf("close journey publisher cursor: %w", err)
		}
		if err := cursor.Err(); err != nil {
			return fmt.Errorf("merge journeys: %w", err)
		}

		result, err := liveCollection.DeleteMany(ctx, staleDatasetRecordsFilter(datasetID, currentTimestamps))
		if err != nil {
			return fmt.Errorf("delete stale journeys: %w", err)
		}
		deleted = result.DeletedCount
		return nil
	}, datasetID)
	return deleted, err
}

func currentDatasetTimestamps(ctx context.Context, rawCollection *mongo.Collection, datasetFilter bson.M) ([]string, error) {
	values, err := rawCollection.Distinct(ctx, "datasource.timestamp", datasetFilter)
	if err != nil {
		return nil, fmt.Errorf("load current raw dataset timestamps: %w", err)
	}
	timestamps := make([]string, 0, len(values))
	for _, value := range values {
		timestamp, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("invalid raw dataset timestamp type %T", value)
		}
		timestamps = append(timestamps, timestamp)
	}
	return timestamps, nil
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
