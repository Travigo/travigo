package datalinker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/travigo/travigo/pkg/ctdf"
	"slices"
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	linkedStopChangeStateID = "linked-stop-changes"
	linkedStopChangeHistory = "linked_stop_change_history"
)

type linkedStopChangeState struct {
	ID            string    `bson:"_id"`
	Building      bool      `bson:"building"`
	Token         string    `bson:"token"`
	Revision      string    `bson:"revision"`
	StartedAt     time.Time `bson:"startedat"`
	ForceFullScan bool      `bson:"forcefullscan"`
	Staging       string    `bson:"staging"`
	LiveToken     string    `bson:"livetoken"`
	LiveEmpty     bool      `bson:"liveempty"`
}

type linkedStopChangeRecord struct {
	ID        string    `bson:"_id"`
	Previous  string    `bson:"previous"`
	IDs       []string  `bson:"ids"`
	FullScan  bool      `bson:"fullscan"`
	CreatedAt time.Time `bson:"createdat"`
}

const linkedStopChangeMaxIDsPerRevision = 5000

func beginLinkedStopChangePublication(ctx context.Context) (string, string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", fmt.Errorf("create stop-change publication token: %w", err)
	}
	token := hex.EncodeToString(bytes)
	collection := database.GetCollection("linked_stop_change_state")
	var old linkedStopChangeState
	err := collection.FindOne(ctx, bson.M{"_id": linkedStopChangeStateID}).Decode(&old)
	forceFullScan := false
	filter := bson.M{"_id": linkedStopChangeStateID}
	staging := "stops_staging_" + token
	if err == nil {
		if old.Building {
			if old.StartedAt.IsZero() || time.Since(old.StartedAt) < time.Hour {
				return "", "", fmt.Errorf("stop-linker change publication is already in progress")
			}
			forceFullScan = true
			filter["token"] = old.Token
			filter["startedat"] = old.StartedAt
		} else {
			filter["building"] = bson.M{"$ne": true}
			if old.Token != "" {
				filter["token"] = old.Token
			} else {
				filter["token"] = bson.M{"$in": bson.A{nil, ""}}
			}
			forceFullScan = old.ForceFullScan
			var live struct {
				Token string `bson:"_linkerpublicationtoken"`
			}
			liveErr := database.GetCollection("stops").FindOne(ctx, bson.M{}, options.FindOne().SetProjection(bson.M{"_id": 0, "_linkerpublicationtoken": 1})).Decode(&live)
			liveFound := liveErr == nil
			if liveErr != nil && liveErr != mongo.ErrNoDocuments {
				return "", "", fmt.Errorf("read live stop token before publication: %w", liveErr)
			}
			if !linkedStopLiveGenerationMatches(old.LiveToken, live.Token, liveFound, old.LiveEmpty) {
				forceFullScan = true
			}
		}
	} else if err != mongo.ErrNoDocuments {
		return "", "", fmt.Errorf("read stop-change state before publication: %w", err)
	}
	set := bson.M{"building": true, "token": token, "startedat": time.Now(), "forcefullscan": forceFullScan, "staging": staging}
	var result *mongo.UpdateResult
	if err == mongo.ErrNoDocuments {
		// Insert rather than upsert: the _id uniqueness constraint fences two
		// first-time publishers without manufacturing an incomplete lock row.
		_, err = collection.InsertOne(ctx, bson.M{"_id": linkedStopChangeStateID, "revision": "", "building": true, "token": token, "startedat": time.Now(), "forcefullscan": true, "staging": staging})
		if err != nil {
			return "", "", fmt.Errorf("acquire initial stop-change publication: %w", err)
		}
		forceFullScan = true
	} else {
		result, err = collection.UpdateOne(ctx, filter, bson.M{"$set": set})
	}
	if err != nil {
		return "", "", fmt.Errorf("mark stop-change publication in progress: %w", err)
	}
	if result != nil && result.MatchedCount != 1 {
		return "", "", fmt.Errorf("stop-linker change publication state changed concurrently")
	}
	if old.Token != "" && old.Staging == "stops_staging_"+old.Token && old.Staging != staging && forceFullScan {
		// The state CAS fenced the dead owner before its token-specific staging
		// collection is removed.
		_ = database.GetCollection(old.Staging).Drop(ctx)
	}
	return token, staging, nil
}

func maintainLinkedStopChangeLease(ctx context.Context, token string) func() {
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_, _ = database.GetCollection("linked_stop_change_state").UpdateOne(ctx,
					bson.M{"_id": linkedStopChangeStateID, "token": token, "building": true},
					bson.M{"$set": bson.M{"startedat": time.Now()}})
			}
		}
	}()
	return func() { close(stop); <-done }
}

func publishLinkedStops(ctx context.Context, staged, live *mongo.Collection, token string) error {
	stateCollection := database.GetCollection("linked_stop_change_state")
	owner := bson.M{"_id": linkedStopChangeStateID, "token": token, "building": true}
	var state linkedStopChangeState
	if err := stateCollection.FindOne(ctx, owner).Decode(&state); err != nil {
		return fmt.Errorf("read stop-linker lease: %w", err)
	}
	full := state.ForceFullScan || state.Revision == ""
	var ids []string
	if !full {
		var err error
		ids, full, err = changedLinkedStopIDs(ctx, live, staged)
		if err != nil {
			return err
		}
	}
	// Check again after the potentially long scan, immediately before $out.
	if err := stateCollection.FindOne(ctx, owner).Err(); err != nil {
		return fmt.Errorf("lost stop-linker lease before publication: %w", err)
	}
	err := staged.FindOne(ctx, bson.M{}, options.FindOne().SetProjection(bson.M{"_id": 1})).Err()
	empty := errors.Is(err, mongo.ErrNoDocuments)
	if err != nil && !empty {
		return err
	}
	if err := copyLinkedStopsWithPublicationToken(ctx, staged, live.Name(), token); err != nil {
		return err
	}
	return finishLinkedStopChangePublication(ctx, token, ids, full, empty)
}

// A failed publisher must release its lease promptly. Its partial work cannot
// be represented by a delta, so the next successful publication creates a
// full-scan revision.
func releaseLinkedStopChangePublication(ctx context.Context, token string) error {
	result, err := database.GetCollection("linked_stop_change_state").UpdateOne(ctx,
		bson.M{"_id": linkedStopChangeStateID, "token": token, "building": true},
		bson.M{"$set": bson.M{"building": false, "forcefullscan": true}})
	if err != nil {
		return fmt.Errorf("release stop-change publication lease: %w", err)
	}
	if result.MatchedCount == 0 {
		return nil // A newer publisher already fenced this token.
	}
	return nil
}

// ReadLinkedStopChanges returns canonical stop IDs changed since previous. A
// full scan is required when the cursor is empty or its history is unavailable.
// It returns an error while a stop-linker publication is in progress; callers
// should retry and verify the returned revision again before checkpointing.
func ReadLinkedStopChanges(ctx context.Context, previous string) (ids []string, revision string, fullScan bool, err error) {
	var state linkedStopChangeState
	err = database.GetCollection("linked_stop_change_state").FindOne(ctx, bson.M{"_id": linkedStopChangeStateID}).Decode(&state)
	if err == mongo.ErrNoDocuments {
		return nil, "", true, nil
	}
	if err != nil {
		return nil, "", false, fmt.Errorf("read stop-change state: %w", err)
	}
	if state.Building {
		return nil, state.Revision, false, fmt.Errorf("stop-linker change publication is in progress")
	}
	revision = state.Revision
	if state.LiveToken != "" {
		var live struct {
			ID    string `bson:"_id"`
			Token string `bson:"_linkerpublicationtoken"`
		}
		findErr := database.GetCollection("stops").FindOne(ctx, bson.M{}, options.FindOne().SetProjection(bson.M{"_id": 0, "_linkerpublicationtoken": 1})).Decode(&live)
		liveMatches := false
		if findErr == mongo.ErrNoDocuments {
			liveMatches = linkedStopLiveGenerationMatches(state.LiveToken, "", false, state.LiveEmpty)
		} else if findErr != nil {
			return nil, revision, false, fmt.Errorf("read live stop publication token: %w", findErr)
		} else {
			liveMatches = linkedStopLiveGenerationMatches(state.LiveToken, live.Token, true, state.LiveEmpty)
		}
		if !liveMatches {
			actual := live.Token
			if findErr == mongo.ErrNoDocuments {
				actual = "empty"
			}
			return nil, linkedStopMismatchRevision(revision, actual), true, nil
		}
	}
	if previous == revision {
		return []string{}, revision, false, nil
	}
	if previous == "" || revision == "" {
		return nil, revision, true, nil
	}
	history := database.GetCollection(linkedStopChangeHistory)
	seen := map[string]struct{}{}
	cursor := revision
	for cursor != previous {
		var record linkedStopChangeRecord
		if err := history.FindOne(ctx, bson.M{"_id": cursor}).Decode(&record); err != nil {
			if err == mongo.ErrNoDocuments {
				return nil, revision, true, nil
			}
			return nil, revision, false, fmt.Errorf("read stop-change revision %s: %w", cursor, err)
		}
		for _, id := range record.IDs {
			seen[id] = struct{}{}
		}
		if record.FullScan {
			return nil, revision, true, nil
		}
		if record.Previous == "" {
			return nil, revision, true, nil
		}
		cursor = record.Previous
	}
	ids = make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, revision, false, nil
}

func linkedStopLiveGenerationMatches(expected, observed string, found, expectedEmpty bool) bool {
	if expected == "" { // pre-token legacy state
		return true
	}
	if !found {
		return expectedEmpty
	}
	return observed == expected
}

func linkedStopMismatchRevision(revision, observed string) string {
	if observed == "" {
		observed = "legacy"
	}
	return revision + ":live-mismatch:" + observed
}

func finishLinkedStopChangePublication(ctx context.Context, token string, ids []string, fullScan, liveEmpty bool) error {
	state := database.GetCollection("linked_stop_change_state")
	var current linkedStopChangeState
	if err := state.FindOne(ctx, bson.M{"_id": linkedStopChangeStateID, "token": token, "building": true}).Decode(&current); err != nil {
		return fmt.Errorf("stop-change publication ownership lost: %w", err)
	}
	revisionBytes := make([]byte, 16)
	if _, err := rand.Read(revisionBytes); err != nil {
		return fmt.Errorf("create stop-change revision: %w", err)
	}
	revision := hex.EncodeToString(revisionBytes)
	ids = uniqueSortedStrings(ids)
	fullScan = fullScan || current.ForceFullScan || len(ids) > linkedStopChangeMaxIDsPerRevision
	if fullScan {
		ids = nil
	}
	record := linkedStopChangeRecord{ID: revision, Previous: current.Revision, IDs: ids, FullScan: fullScan, CreatedAt: time.Now()}
	if _, err := database.GetCollection(linkedStopChangeHistory).InsertOne(ctx, record); err != nil {
		return fmt.Errorf("save stop-change revision: %w", err)
	}
	result, err := state.UpdateOne(ctx,
		bson.M{"_id": linkedStopChangeStateID, "token": token, "building": true},
		bson.M{"$set": bson.M{"building": false, "revision": revision, "livetoken": token, "liveempty": liveEmpty, "forcefullscan": false}})
	if err != nil {
		return fmt.Errorf("publish stop-change revision: %w", err)
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("stop-change publication ownership lost before publish")
	}
	_, _ = database.GetCollection(linkedStopChangeHistory).DeleteMany(ctx, bson.M{"createdat": bson.M{"$lt": time.Now().Add(-90 * 24 * time.Hour)}})
	return nil
}

func uniqueSortedStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; !ok {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

// Compare only the fields consumed by transfer generation. Reuse its exact
// eligibility query so a stop becoming inactive also appears as a removal.
func changedLinkedStopIDs(ctx context.Context, existing, staged *mongo.Collection) ([]string, bool, error) {
	before := map[string][sha256.Size]byte{}
	err := scanTransferCollection(ctx, existing, nil, func(stop *ctdf.Stop) error {
		digest, err := linkedTransferDigest(stop)
		if err == nil {
			before[stop.PrimaryIdentifier] = digest
		}
		return err
	})
	if err != nil {
		return nil, false, err
	}
	var changed []string
	overflow := errors.New("stop change journal entry exceeds limit")
	err = scanTransferCollection(ctx, staged, nil, func(stop *ctdf.Stop) error {
		digest, err := linkedTransferDigest(stop)
		if err != nil {
			return err
		}
		prior, ok := before[stop.PrimaryIdentifier]
		if !ok || prior != digest {
			changed = append(changed, stop.PrimaryIdentifier)
		}
		delete(before, stop.PrimaryIdentifier)
		if len(changed) > linkedStopChangeMaxIDsPerRevision {
			return overflow
		}
		return nil
	})
	if errors.Is(err, overflow) {
		return nil, true, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(before)+len(changed) > linkedStopChangeMaxIDsPerRevision {
		return nil, true, nil
	}
	for id := range before {
		changed = append(changed, id)
	}
	return changed, false, nil
}

func linkedTransferDigest(stop *ctdf.Stop) ([sha256.Size]byte, error) {
	fields := struct {
		Stop    transferSnapshot
		Aliases []string
	}{
		Stop:    snapshotForStop(transferStopFromCTDF(stop, 0, slices.Contains(stop.TransportTypes, ctdf.TransportTypeRail))),
		Aliases: uniqueSortedStrings(stop.OtherIdentifiers),
	}
	encoded, err := bson.Marshal(fields)
	if err != nil {
		return [sha256.Size]byte{}, err
	}
	return sha256.Sum256(encoded), nil
}
