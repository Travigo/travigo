package datalinker

import (
	"context"
	"errors"
	"math"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const defaultOplogClearSizeMB = 990

type StopLinkerMongoMaintenanceConfig struct {
	OplogClearSizeMB int
}

func compactLinkedCollections(ctx context.Context, collectionNames ...string) {
	for _, collectionName := range collectionNames {
		if err := compactCollection(ctx, collectionName); err != nil {
			log.Error().Err(err).Str("collection", collectionName).Msg("Failed to compact collection")
		}
	}
}

func runStopLinkerOplogMaintenance(config StopLinkerMongoMaintenanceConfig) {
	ctx := context.Background()

	if err := clearOplog(ctx, config.OplogClearSizeMB); err != nil {
		log.Error().Err(err).Msg("Failed to clear oplog")
	}
}

func compactCollection(ctx context.Context, collectionName string) error {
	log.Info().Str("collection", collectionName).Msg("Compacting collection")

	return database.Instance.Database.RunCommand(ctx, bson.D{
		{Key: "compact", Value: collectionName},
		{Key: "force", Value: true},
	}).Err()
}

func clearOplog(ctx context.Context, requestedSizeMB int) error {
	if requestedSizeMB < defaultOplogClearSizeMB {
		requestedSizeMB = defaultOplogClearSizeMB
	}

	originalSizeMB, err := getOplogMaxSizeMB(ctx)
	if err != nil {
		return err
	}

	clearSizeMB := requestedSizeMB
	if originalSizeMB < clearSizeMB {
		clearSizeMB = originalSizeMB
	}

	log.Warn().
		Int("clear_size_mb", clearSizeMB).
		Int("restore_size_mb", originalSizeMB).
		Msg("Clearing oplog by temporarily shrinking, compacting, and restoring max size")

	if err := resizeOplog(ctx, clearSizeMB); err != nil {
		return err
	}

	if err := compactOplog(ctx); err != nil {
		return err
	}

	if originalSizeMB > clearSizeMB {
		if err := resizeOplog(ctx, originalSizeMB); err != nil {
			return err
		}
	}

	return nil
}

func getOplogMaxSizeMB(ctx context.Context) (int, error) {
	var stats struct {
		MaxSize int64 `bson:"maxSize"`
	}

	if err := database.Instance.Client.Database("local").RunCommand(ctx, bson.D{
		{Key: "collStats", Value: "oplog.rs"},
	}).Decode(&stats); err != nil {
		return 0, err
	}

	if stats.MaxSize <= 0 {
		return 0, errors.New("local.oplog.rs maxSize is unavailable")
	}

	return int(math.Ceil(float64(stats.MaxSize) / (1024 * 1024))), nil
}

func resizeOplog(ctx context.Context, sizeMB int) error {
	log.Warn().Int("size_mb", sizeMB).Msg("Resizing oplog")

	return database.Instance.Client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "replSetResizeOplog", Value: 1},
		{Key: "size", Value: float64(sizeMB)},
		{Key: "minRetentionHours", Value: float64(0)},
	}).Err()
}

func compactOplog(ctx context.Context) error {
	log.Warn().Msg("Compacting local.oplog.rs")

	return database.Instance.Client.Database("local").RunCommand(ctx, bson.D{
		{Key: "compact", Value: "oplog.rs"},
		{Key: "force", Value: true},
	}).Err()
}
