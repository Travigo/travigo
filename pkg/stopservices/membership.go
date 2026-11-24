package stopservices

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// TransferMembership returns the current static stop membership snapshot.
// When previous is non-nil, refs is nil unless an old generation needed for a
// delta has disappeared; changedRefs then contains only membership candidates
// whose global presence changed, with the value indicating current presence.
// membershipActive distinguishes a genuinely empty membership set from a
// disabled filter. The returned maps are tied to revision.
func TransferMembership(ctx context.Context, db *mongo.Database, previous map[string]string) (
	refs map[string]struct{}, generations map[string]string, changedRefs map[string]bool,
	membershipActive bool, revision int64, err error,
) {
	if ctx == nil {
		ctx = context.Background()
	}
	if db == nil {
		return nil, nil, nil, false, 0, errors.New("stop-service database is nil")
	}
	state, err := readState(ctx, db)
	if err != nil {
		return nil, nil, nil, false, 0, fmt.Errorf("read stop-service publication state: %w", err)
	}
	revision = state.EventRevision
	if !stateIsCurrent(state) {
		return nil, nil, nil, false, revision, ErrSourceChanged
	}
	generations = make(map[string]string, len(state.Sources))
	for _, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) {
			continue
		}
		generations[source.Dataset] = source.Generation
	}

	// A first transfer needs the full set. Thereafter only references from
	// datasets whose generations changed need to be considered.
	if previous == nil {
		refs, err = scanCurrentMembership(ctx, db, state)
		if err != nil {
			return nil, nil, nil, false, revision, err
		}
		membershipActive = len(refs) != 0
		if err := confirmMembershipSnapshot(ctx, db, state); err != nil {
			return nil, nil, nil, false, revision, err
		}
		return refs, generations, map[string]bool{}, membershipActive, revision, nil
	}

	changedDatasets := make(map[string]struct{})
	for dataset, generation := range generations {
		if previous[dataset] != generation {
			changedDatasets[dataset] = struct{}{}
		}
	}
	for dataset, generation := range previous {
		if generation != "" && generations[dataset] != generation {
			changedDatasets[dataset] = struct{}{}
		}
	}
	candidates := make(map[string]struct{})
	usedOldGenerations := make(map[string]string)
	for dataset := range changedDatasets {
		var oldRefs map[string]struct{}
		if old := previous[dataset]; old != "" {
			if !generationIsProtected(state, dataset, old) {
				refs, err = scanCurrentMembership(ctx, db, state)
				if err != nil {
					return nil, nil, nil, false, revision, err
				}
				membershipActive = len(refs) != 0
				if err := confirmMembershipSnapshot(ctx, db, state); err != nil {
					return nil, nil, nil, false, revision, err
				}
				return refs, generations, map[string]bool{}, membershipActive, revision, nil
			}
			usedOldGenerations[dataset] = old
			var queryErr error
			oldRefs, queryErr = generationMembership(ctx, db, dataset, old)
			if queryErr != nil {
				return nil, nil, nil, false, revision, queryErr
			}
		}
		var newRefs map[string]struct{}
		if current := generations[dataset]; current != "" {
			var queryErr error
			newRefs, queryErr = generationMembership(ctx, db, dataset, current)
			if queryErr != nil {
				return nil, nil, nil, false, revision, queryErr
			}
		}
		for ref := range oldRefs {
			if _, remains := newRefs[ref]; !remains {
				candidates[ref] = struct{}{}
			}
		}
		for ref := range newRefs {
			if _, existed := oldRefs[ref]; !existed {
				candidates[ref] = struct{}{}
			}
		}
	}

	changedRefs = make(map[string]bool)
	if len(candidates) > 0 {
		_, changedRefs, err = currentPresence(ctx, db, state, candidates)
		if err != nil {
			return nil, nil, nil, false, revision, err
		}
		beforeState := stateDocument{Sources: make(map[string]datasetSourceState, len(previous))}
		for dataset, generation := range previous {
			if dataset == "" || generation == "" || excluded(dataset) {
				continue
			}
			beforeState.Sources[sourceKey(dataset)] = datasetSourceState{Dataset: dataset, Generation: generation}
		}
		_, beforePresence, queryErr := currentPresence(ctx, db, beforeState, candidates)
		if queryErr != nil {
			return nil, nil, nil, false, revision, queryErr
		}
		for ref, wasPresent := range beforePresence {
			if changedRefs[ref] == wasPresent {
				delete(changedRefs, ref)
			}
		}
	}
	membershipActive, err = hasCurrentMembership(ctx, db, state)
	if err != nil {
		return nil, nil, nil, false, revision, err
	}
	if err := confirmMembershipSnapshot(ctx, db, state, usedOldGenerations); err != nil {
		return nil, nil, nil, false, revision, err
	}
	return nil, generations, changedRefs, membershipActive, revision, nil
}

func scanCurrentMembership(ctx context.Context, db *mongo.Database, state stateDocument) (map[string]struct{}, error) {
	clauses := currentGenerationClauses(state)
	refs := make(map[string]struct{})
	if len(clauses) == 0 {
		return refs, nil
	}
	cur, err := db.Collection(RefsCollectionName).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"$or": clauses}}},
		{{Key: "$group", Value: bson.M{"_id": "$stopref"}}},
	}, options.Aggregate().SetAllowDiskUse(true).SetBatchSize(10000))
	if err != nil {
		return nil, fmt.Errorf("aggregate current stop-service membership: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			Ref string `bson:"_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode current stop-service membership: %w", err)
		}
		if row.Ref != "" {
			refs[row.Ref] = struct{}{}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("iterate current stop-service membership: %w", err)
	}
	return refs, nil
}

func currentGenerationClauses(state stateDocument) bson.A {
	clauses := make(bson.A, 0, len(state.Sources))
	for _, source := range state.Sources {
		if source.Dataset != "" && source.Generation != "" && !excluded(source.Dataset) {
			clauses = append(clauses, bson.M{"dataset": source.Dataset, "generation": source.Generation})
		}
	}
	return clauses
}

func generationIsProtected(state stateDocument, dataset, generation string) bool {
	key := sourceKey(dataset)
	if source, ok := state.Sources[key]; ok && source.Dataset == dataset && source.Generation == generation {
		return true
	}
	for _, retired := range state.Retired {
		if retired.Dataset == dataset && retired.Generation == generation {
			return retired.RetiredAt.After(time.Now().UTC().Add(-GenerationGrace))
		}
	}
	return false
}

func generationMembership(ctx context.Context, db *mongo.Database, dataset, generation string) (map[string]struct{}, error) {
	cur, err := db.Collection(RefsCollectionName).Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"dataset": dataset, "generation": generation}}},
		{{Key: "$group", Value: bson.M{"_id": "$stopref"}}},
	}, options.Aggregate().SetAllowDiskUse(true).SetBatchSize(10000))
	if err != nil {
		return nil, fmt.Errorf("query stop-service membership for %q/%q: %w", dataset, generation, err)
	}
	refs := make(map[string]struct{})
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			Ref string `bson:"_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, fmt.Errorf("decode stop-service membership for %q/%q: %w", dataset, generation, err)
		}
		if row.Ref != "" {
			refs[row.Ref] = struct{}{}
		}
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("iterate stop-service membership for %q/%q: %w", dataset, generation, err)
	}
	return refs, nil
}

func currentPresence(ctx context.Context, db *mongo.Database, state stateDocument, candidates map[string]struct{}) (bool, map[string]bool, error) {
	changed := make(map[string]bool, len(candidates))
	for ref := range candidates {
		changed[ref] = false
	}
	refs := make([]string, 0, len(candidates))
	for ref := range candidates {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	clauses := currentGenerationClauses(state)
	if len(clauses) == 0 || len(refs) == 0 {
		return false, changed, nil
	}
	for start := 0; start < len(refs); start += DefaultBatchSize {
		end := min(start+DefaultBatchSize, len(refs))
		cur, err := db.Collection(RefsCollectionName).Aggregate(ctx, mongo.Pipeline{
			{{Key: "$match", Value: bson.M{"stopref": bson.M{"$in": refs[start:end]}, "$or": clauses}}},
			{{Key: "$group", Value: bson.M{"_id": "$stopref"}}},
		}, options.Aggregate().SetAllowDiskUse(true).SetBatchSize(10000))
		if err != nil {
			return false, nil, fmt.Errorf("query current stop-service membership candidates: %w", err)
		}
		for cur.Next(ctx) {
			var row struct {
				Ref string `bson:"_id"`
			}
			if err := cur.Decode(&row); err != nil {
				cur.Close(ctx)
				return false, nil, fmt.Errorf("decode current stop-service membership candidate: %w", err)
			}
			if _, ok := changed[row.Ref]; ok {
				changed[row.Ref] = true
			}
		}
		if err := cur.Err(); err != nil {
			cur.Close(ctx)
			return false, nil, fmt.Errorf("iterate current stop-service membership candidates: %w", err)
		}
		if err := cur.Close(ctx); err != nil {
			return false, nil, fmt.Errorf("close current stop-service membership candidates: %w", err)
		}
	}
	return true, changed, nil
}

func hasCurrentMembership(ctx context.Context, db *mongo.Database, state stateDocument) (bool, error) {
	clauses := currentGenerationClauses(state)
	if len(clauses) == 0 {
		return false, nil
	}
	err := db.Collection(RefsCollectionName).FindOne(ctx, bson.M{"$or": clauses}, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 0}, {Key: "stopref", Value: 1}})).Err()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check current stop-service membership: %w", err)
	}
	return true, nil
}

func confirmMembershipSnapshot(ctx context.Context, db *mongo.Database, before stateDocument, oldGenerations ...map[string]string) error {
	after, err := readState(ctx, db)
	if err != nil {
		return fmt.Errorf("recheck stop-service publication state: %w", err)
	}
	if !stateIsCurrent(after) || after.EventRevision != before.EventRevision {
		return ErrSourceChanged
	}
	for _, generations := range oldGenerations {
		for dataset, generation := range generations {
			if !generationIsProtected(after, dataset, generation) {
				return ErrSourceChanged
			}
		}
	}
	return nil
}
