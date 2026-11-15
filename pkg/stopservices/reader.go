package stopservices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"sort"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// IsCurrent reports whether the published static view covers the latest
// completed source writes.
func IsCurrent(ctx context.Context, db *mongo.Database) (bool, error) {
	state, err := readState(ctx, db)
	if errors.Is(err, ErrUnbuilt) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return stateIsCurrent(state), nil
}

// CurrentRevision returns the source event revision and whether the published
// stop-service view covers all completed source writes.
func CurrentRevision(ctx context.Context, db *mongo.Database) (int64, bool, error) {
	state, err := readState(ctx, db)
	if errors.Is(err, ErrUnbuilt) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return state.EventRevision, stateIsCurrent(state), nil
}

// CurrentStopRefs returns the stop references present in the current published
// static service view, a deterministic membership hash, and its event revision.
// The source revision is checked again after the scan so callers never treat a
// mixed-generation result as a stable snapshot.
func CurrentStopRefs(ctx context.Context, db *mongo.Database, expectedRevision int64) (map[string]struct{}, string, int64, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return nil, "", 0, errors.New("stop-service database is nil")
	}

	state, err := readState(ctx, db)
	if err != nil {
		return nil, "", 0, fmt.Errorf("read stop-service publication state: %w", err)
	}
	if !stateIsCurrent(state) || state.EventRevision != expectedRevision {
		return nil, "", state.EventRevision, ErrSourceChanged
	}

	generations := make([]bson.M, 0, len(state.Sources))
	for _, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) {
			continue
		}
		generations = append(generations, bson.M{
			"dataset":    source.Dataset,
			"generation": source.Generation,
		})
	}

	stopRefs := make(map[string]struct{})
	hasher := sha256.New()
	if len(generations) > 0 {
		pipeline := mongo.Pipeline{
			{{Key: "$match", Value: bson.M{"$or": generations}}},
			{{Key: "$group", Value: bson.M{"_id": "$stopref"}}},
			{{Key: "$sort", Value: bson.D{{Key: "_id", Value: 1}}}},
		}
		aggregateOptions := options.Aggregate().SetAllowDiskUse(true).SetBatchSize(10000)
		cursor, err := db.Collection(RefsCollectionName).Aggregate(ctx, pipeline, aggregateOptions)
		if err != nil {
			return nil, "", state.EventRevision, fmt.Errorf("aggregate current stop-service references: %w", err)
		}
		for cursor.Next(ctx) {
			var row struct {
				StopRef string `bson:"_id"`
			}
			if err := cursor.Decode(&row); err != nil {
				cursor.Close(ctx)
				return nil, "", state.EventRevision, fmt.Errorf("decode current stop-service reference: %w", err)
			}
			if row.StopRef == "" {
				continue
			}
			stopRefs[row.StopRef] = struct{}{}
			writeStopRefHash(hasher, row.StopRef)
		}
		if err := cursor.Err(); err != nil {
			cursor.Close(ctx)
			return nil, "", state.EventRevision, fmt.Errorf("iterate current stop-service references: %w", err)
		}
		if err := cursor.Close(ctx); err != nil {
			return nil, "", state.EventRevision, fmt.Errorf("close current stop-service references cursor: %w", err)
		}
	}

	after, err := readState(ctx, db)
	if err != nil {
		return nil, "", state.EventRevision, fmt.Errorf("recheck stop-service publication state: %w", err)
	}
	if !stateIsCurrent(after) || after.EventRevision != expectedRevision {
		return nil, "", after.EventRevision, ErrSourceChanged
	}

	return stopRefs, hex.EncodeToString(hasher.Sum(nil)), expectedRevision, nil
}

func stateIsCurrent(state stateDocument) bool {
	if !state.Initialized || state.FullActiveWriters != 0 || state.FullRevision != state.FullPublishedRevision {
		return false
	}
	for _, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) {
			continue
		}
		if source.Generation == "" || source.ActiveWriters != 0 || source.Revision != source.PublishedRevision {
			return false
		}
	}
	return true
}

func writeStopRefHash(hasher hash.Hash, stopRef string) {
	_, _ = hasher.Write([]byte(stopRef))
	_, _ = hasher.Write([]byte{0})
}

// BatchForStops resolves the published raw service references for a set of
// primary, alias, and platform stop references. It reads one manifest snapshot
// and queries only the generations named by that snapshot. Every requested
// stop reference is present in the returned map, including stops with no
// services.
func BatchForStops(ctx context.Context, db *mongo.Database, stopRefs []string) (map[string][]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return nil, errors.New("stop-service database is nil")
	}
	result := make(map[string][]string, len(stopRefs))
	requested := make(map[string]struct{}, len(stopRefs))
	for _, stopRef := range stopRefs {
		if stopRef == "" {
			continue
		}
		if _, exists := requested[stopRef]; exists {
			continue
		}
		requested[stopRef] = struct{}{}
		result[stopRef] = []string{}
	}
	if len(requested) == 0 {
		return result, nil
	}

	state, err := readState(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("read stop-service publication state: %w", err)
	}
	if !state.Initialized {
		return nil, ErrUnbuilt
	}
	type generationRef struct {
		dataset    string
		generation string
	}
	generations := make([]generationRef, 0, len(state.Sources))
	hasUnpublishedSource := false
	for _, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) {
			continue
		}
		if source.Generation == "" {
			hasUnpublishedSource = true
			// A full backfill can publish dataset generations incrementally.
			// Keep serving contributions that are ready while this dataset is
			// still unbuilt; one missing contribution must not block every stop.
			continue
		}
		generations = append(generations, generationRef{dataset: source.Dataset, generation: source.Generation})
	}
	if len(generations) == 0 {
		if hasUnpublishedSource {
			return nil, ErrUnbuilt
		}
		return result, nil
	}

	stopIDs := make([]string, 0, len(requested))
	for stopRef := range requested {
		stopIDs = append(stopIDs, stopRef)
	}
	seenByStop := make(map[string]map[string]struct{}, len(stopIDs))
	refs := db.Collection(RefsCollectionName)
	projection := bson.D{
		{Key: "_id", Value: 0},
		{Key: "stopref", Value: 1},
		{Key: "serviceref", Value: 1},
	}
	for start := 0; start < len(stopIDs); start += DefaultBatchSize {
		end := min(start+DefaultBatchSize, len(stopIDs))
		clauses := make(bson.A, 0, len(generations))
		for _, generation := range generations {
			clauses = append(clauses, bson.M{
				"dataset":    generation.dataset,
				"generation": generation.generation,
			})
		}
		filter := bson.M{
			"stopref": bson.M{"$in": stopIDs[start:end]},
			"$or":     clauses,
		}
		cursor, err := refs.Find(ctx, filter, options.Find().SetProjection(projection))
		if err != nil {
			return nil, fmt.Errorf("query stop-service references: %w", err)
		}
		for cursor.Next(ctx) {
			var row struct {
				StopRef    string `bson:"stopref"`
				ServiceRef string `bson:"serviceref"`
			}
			if err := cursor.Decode(&row); err != nil {
				cursor.Close(ctx)
				return nil, fmt.Errorf("decode stop-service reference: %w", err)
			}
			if row.StopRef == "" || row.ServiceRef == "" {
				continue
			}
			if seenByStop[row.StopRef] == nil {
				seenByStop[row.StopRef] = make(map[string]struct{})
			}
			if _, exists := seenByStop[row.StopRef][row.ServiceRef]; exists {
				continue
			}
			seenByStop[row.StopRef][row.ServiceRef] = struct{}{}
			result[row.StopRef] = append(result[row.StopRef], row.ServiceRef)
		}
		if err := cursor.Err(); err != nil {
			cursor.Close(ctx)
			return nil, fmt.Errorf("iterate stop-service references: %w", err)
		}
		if err := cursor.Close(ctx); err != nil {
			return nil, fmt.Errorf("close stop-service references cursor: %w", err)
		}
	}

	for stopRef := range result {
		sort.Strings(result[stopRef])
	}
	return result, nil
}
