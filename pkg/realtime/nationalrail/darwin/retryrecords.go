package darwin

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	darwinRetryInterval = 90 * time.Second
	// darwinRetryBatchSize bounds one retry scan so a large expired backlog
	// cannot be loaded into memory at once. Oldest records are retried first.
	darwinRetryBatchSize = 1000
)

type darwinFormationRetryRecord struct {
	ID               primitive.ObjectID `bson:"_id"`
	CreationDateTime time.Time          `bson:"creationdatetime"`
	Record           ScheduleFormations `bson:"record"`
}

type darwinFormationRetry struct {
	record     darwinFormationRetryRecord
	superseded []primitive.ObjectID
}

func RetryRecords(dispatcher workDispatcher) {
	retryRecordsCollection := database.GetCollection("retry_records")

	for {
		retries, err := pendingDarwinFormationRetries(context.Background(), retryRecordsCollection)
		if err != nil {
			log.Error().Err(err).Msg("Failed to load Darwin formation retry records")
			time.Sleep(darwinRetryInterval)
			continue
		}

		for _, retry := range retries {
			retry := retry
			// Submit on the same shard as live formation updates so a retry
			// cannot overtake a newer formation for the same run.
			dispatcher.Submit(darwinFormationKey(retry.record.Record.RID), func() {
				if err := applyDarwinScheduleFormation(context.Background(), retry.record.Record); err != nil {
					// Expected while the schedule has not arrived yet. The live
					// path already logged the original failure.
					log.Debug().
						Err(err).
						Str("rid", retry.record.Record.RID).
						Msg("Darwin formation retry is not ready")

					// Keep the record so the next pass can retry it.
					return
				}

				consumedIDs := append([]primitive.ObjectID{retry.record.ID}, retry.superseded...)
				if _, err := retryRecordsCollection.DeleteMany(
					context.Background(),
					bson.M{"_id": bson.M{"$in": consumedIDs}},
				); err != nil {
					log.Error().
						Err(err).
						Str("rid", retry.record.Record.RID).
						Msg("Failed to delete consumed Darwin formation retry records")
				}
			})
		}

		time.Sleep(darwinRetryInterval)
	}
}

// pendingDarwinFormationRetries returns the newest retry record per run. Older
// records for the same run are superseded and deleted with it once the write
// succeeds, so the same formation is not replayed until the TTL expires.
func pendingDarwinFormationRetries(ctx context.Context, collection *mongo.Collection) ([]darwinFormationRetry, error) {
	cursor, err := collection.Find(
		ctx,
		bson.M{"type": "realtimedarwin_formation"},
		options.Find().
			SetSort(bson.D{{Key: "creationdatetime", Value: 1}}).
			SetLimit(darwinRetryBatchSize).
			SetProjection(bson.M{"_id": 1, "creationdatetime": 1, "record": 1}),
	)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var records []darwinFormationRetryRecord
	if err := cursor.All(ctx, &records); err != nil {
		return nil, err
	}

	return dedupeDarwinFormationRetries(records), nil
}

func dedupeDarwinFormationRetries(records []darwinFormationRetryRecord) []darwinFormationRetry {
	latestByRun := make(map[string]darwinFormationRetry, len(records))
	runOrder := make([]string, 0, len(records))
	for _, record := range records {
		rid := record.Record.RID
		if rid == "" || len(record.Record.Formations) == 0 {
			continue
		}

		if existing, found := latestByRun[rid]; found {
			existing.superseded = append(existing.superseded, existing.record.ID)
			existing.record = record
			latestByRun[rid] = existing
			continue
		}

		latestByRun[rid] = darwinFormationRetry{record: record}
		runOrder = append(runOrder, rid)
	}

	retries := make([]darwinFormationRetry, 0, len(runOrder))
	for _, rid := range runOrder {
		retries = append(retries, latestByRun[rid])
	}

	return retries
}
