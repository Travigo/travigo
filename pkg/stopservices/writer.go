package stopservices

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// BeginSourceWrite marks journey documents as changing before the publisher
// touches them. Pass one or more static dataset IDs for scoped imports; pass no
// IDs when publishing the complete live journey collection.
//
// The returned function must be deferred and called even when publication
// fails, because partially written live data also needs a materialized refresh.
func BeginSourceWrite(ctx context.Context, db *mongo.Database, datasetIDs ...string) (func(context.Context) error, error) {
	if db == nil {
		return nil, errors.New("stop-service database is nil")
	}
	if err := ensureStateDocument(ctx, db); err != nil {
		return nil, err
	}

	if len(datasetIDs) == 0 {
		if _, err := db.Collection(StateCollectionName).UpdateOne(ctx, bson.M{"_id": stateDocumentID}, bson.M{
			"$inc": bson.M{"eventRevision": 1, "fullRevision": 1, "fullActiveWriters": 1},
		}); err != nil {
			return nil, fmt.Errorf("mark full journey publish started: %w", err)
		}
		return onceFinish(func(finishCtx context.Context) error {
			result, err := db.Collection(StateCollectionName).UpdateOne(finishCtx,
				bson.M{"_id": stateDocumentID, "fullActiveWriters": bson.M{"$gt": 0}},
				bson.M{"$inc": bson.M{"eventRevision": 1, "fullRevision": 1, "fullActiveWriters": -1}},
			)
			if err != nil {
				return fmt.Errorf("mark full journey publish finished: %w", err)
			}
			if result.MatchedCount != 1 {
				return errors.New("full journey publish marker was not active at finish")
			}
			return nil
		}), nil
	}

	uniqueIDs := make([]string, 0, len(datasetIDs))
	seen := make(map[string]struct{}, len(datasetIDs))
	for _, dataset := range datasetIDs {
		if dataset == "" {
			return nil, errors.New("stop-service source dataset ID is empty")
		}
		if excluded(dataset) {
			continue
		}
		if _, ok := seen[dataset]; ok {
			continue
		}
		seen[dataset] = struct{}{}
		uniqueIDs = append(uniqueIDs, dataset)
	}
	if len(uniqueIDs) == 0 {
		return func(context.Context) error { return nil }, nil
	}

	acquired := make([]string, 0, len(uniqueIDs))
	for _, dataset := range uniqueIDs {
		if err := registerDataset(ctx, db, dataset); err != nil {
			finishCtx, cancel := cleanupContext(ctx)
			finishErr := finishDatasetWrites(finishCtx, db, acquired)
			cancel()
			return nil, errors.Join(fmt.Errorf("register stop-service source %q: %w", dataset, err), finishErr)
		}
		key := sourceKey(dataset)
		_, err := db.Collection(StateCollectionName).UpdateOne(ctx, bson.M{"_id": stateDocumentID}, bson.M{
			"$set": bson.M{"sources." + key + ".dataset": dataset},
			"$inc": bson.M{
				"eventRevision":                     1,
				"sources." + key + ".revision":      1,
				"sources." + key + ".activeWriters": 1,
			},
		})
		if err != nil {
			finishCtx, cancel := cleanupContext(ctx)
			finishErr := finishDatasetWrites(finishCtx, db, acquired)
			cancel()
			return nil, errors.Join(fmt.Errorf("mark journey publish started for %q: %w", dataset, err), finishErr)
		}
		acquired = append(acquired, dataset)
	}

	return onceFinish(func(finishCtx context.Context) error {
		return finishDatasetWrites(finishCtx, db, acquired)
	}), nil
}

func registerDataset(ctx context.Context, db *mongo.Database, dataset string) error {
	key := sourceKey(dataset)
	filter := bson.M{
		"_id":            stateDocumentID,
		"sources." + key: bson.M{"$exists": false},
	}
	update := bson.M{
		"$set": bson.M{
			"sources." + key + ".dataset":           dataset,
			"sources." + key + ".revision":          int64(0),
			"sources." + key + ".activeWriters":     int64(0),
			"sources." + key + ".publishedRevision": int64(-1),
		},
		"$inc": bson.M{"eventRevision": 1, "catalogRevision": 1},
	}
	if _, err := db.Collection(StateCollectionName).UpdateOne(ctx, filter, update); err != nil {
		return err
	}
	return nil
}

func finishDatasetWrites(ctx context.Context, db *mongo.Database, datasets []string) error {
	var errs []error
	for _, dataset := range datasets {
		key := sourceKey(dataset)
		result, err := db.Collection(StateCollectionName).UpdateOne(ctx,
			bson.M{"_id": stateDocumentID, "sources." + key + ".activeWriters": bson.M{"$gt": 0}},
			bson.M{"$inc": bson.M{
				"eventRevision":                     1,
				"sources." + key + ".revision":      1,
				"sources." + key + ".activeWriters": -1,
			}},
		)
		if err != nil {
			errs = append(errs, fmt.Errorf("mark journey publish finished for %q: %w", dataset, err))
			continue
		}
		if result.MatchedCount != 1 {
			errs = append(errs, fmt.Errorf("journey publish marker for %q was not active at finish", dataset))
		}
	}
	return errors.Join(errs...)
}

func onceFinish(finish func(context.Context) error) func(context.Context) error {
	var once sync.Once
	var finishErr error
	return func(ctx context.Context) error {
		once.Do(func() {
			finishCtx, cancel := cleanupContext(ctx)
			defer cancel()
			finishErr = finish(finishCtx)
		})
		return finishErr
	}
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
}

// RetireDatasets withdraws contributions after their published journeys have
// been deleted. Retiring an absent source is a no-op, and retries can withdraw
// a source even if its journeys or an earlier writer are already gone.
func RetireDatasets(ctx context.Context, db *mongo.Database, datasets []string) error {
	state, err := readState(ctx, db)
	if errors.Is(err, ErrUnbuilt) {
		return nil
	}
	if err != nil {
		return err
	}
	filter := bson.M{"_id": stateDocumentID, "eventRevision": state.EventRevision}
	unset := bson.M{}
	var retired []retiredGeneration
	now := time.Now().UTC()
	for _, dataset := range datasets {
		key := sourceKey(dataset)
		source, exists := state.Sources[key]
		if !exists {
			continue
		}
		path := "sources." + key
		unset[path] = ""
		if source.Generation != "" {
			filter[path+".generation"] = source.Generation
			retired = append(retired, retiredGeneration{
				Dataset: dataset, Generation: source.Generation, RetiredAt: now,
			})
		} else {
			filter[path+".generation"] = bson.M{"$in": bson.A{nil, ""}}
		}
	}
	if len(unset) == 0 {
		return nil
	}
	update := bson.M{
		"$unset": unset,
		"$inc":   bson.M{"eventRevision": 1, "catalogRevision": 1},
	}
	if len(retired) != 0 {
		update["$push"] = bson.M{"retired": bson.M{"$each": retired}}
	}
	result, err := db.Collection(StateCollectionName).UpdateOne(ctx, filter, update)
	if err != nil {
		return err
	}
	if result.MatchedCount != 1 {
		return ErrSourceChanged
	}
	return nil
}

// RecoverSourceWriters is an explicit operator action after confirming no
// journey publisher is still running. It resets crash-stuck counters and
// increments revisions so every affected dataset remains pending for rebuild.
func RecoverSourceWriters(ctx context.Context, db *mongo.Database) error {
	if err := ensureStateDocument(ctx, db); err != nil {
		return err
	}
	state, err := readState(ctx, db)
	if err != nil {
		return err
	}
	update := bson.M{"$inc": bson.M{"eventRevision": int64(1), "fullRevision": 1}, "$set": bson.M{"fullActiveWriters": int64(0)}}
	for key, source := range state.Sources {
		if source.ActiveWriters == 0 {
			continue
		}
		update["$set"].(bson.M)["sources."+key+".activeWriters"] = int64(0)
		update["$inc"].(bson.M)["sources."+key+".revision"] = source.ActiveWriters + 1
		update["$inc"].(bson.M)["eventRevision"] = update["$inc"].(bson.M)["eventRevision"].(int64) + source.ActiveWriters
	}
	result, err := db.Collection(StateCollectionName).UpdateOne(ctx,
		bson.M{"_id": stateDocumentID, "eventRevision": state.EventRevision},
		update,
	)
	if err != nil {
		return fmt.Errorf("recover stop-service source markers: %w", err)
	}
	if result.MatchedCount != 1 {
		return ErrSourceChanged
	}
	return nil
}
