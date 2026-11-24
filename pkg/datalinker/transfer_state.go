package datalinker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	transferSnapshotCollection     = "stop_transfer_snapshots"
	transferDirtyCollection        = "stop_transfer_dirty_origins"
	transferBuildStateCollection   = "stop_transfer_build_state"
	transferBuildLeaseID           = "stop-transfer-build"
	transferBuildLeaseDuration     = 2 * time.Minute
	transferBuildHeartbeatInterval = 30 * time.Second
	transferDirtyBatchSize         = 500
)

// transferBuildLease is an expiring, token-fenced lease. A worker must run
// every output mutation in WithBatch; that transaction verifies and extends
// the lease before invoking its callback.
type transferBuildLease struct {
	db            *mongo.Database
	token         string
	mu            sync.Mutex
	stopHeartbeat context.CancelFunc
	heartbeatDone chan struct{}
}

type transferBuildState struct {
	ID        string    `bson:"_id"`
	Token     string    `bson:"token"`
	ExpiresAt time.Time `bson:"expires_at"`
}

func acquireTransferBuild(ctx context.Context) (*transferBuildLease, error) {
	if database.Instance == nil || database.Instance.Database == nil {
		return nil, errors.New("acquire transfer build lease: database is not connected")
	}
	var random [24]byte
	if _, err := rand.Read(random[:]); err != nil {
		return nil, fmt.Errorf("create transfer build token: %w", err)
	}
	token := hex.EncodeToString(random[:])
	coll := database.Instance.Database.Collection(transferBuildStateCollection)
	now := time.Now()
	filter := bson.M{"_id": transferBuildLeaseID, "$or": bson.A{
		bson.M{"expires_at": bson.M{"$lte": now}}, bson.M{"token": bson.M{"$exists": false}},
	}}
	update := bson.M{"$set": bson.M{"token": token, "expires_at": now.Add(transferBuildLeaseDuration)}}
	var state transferBuildState
	err := coll.FindOneAndUpdate(ctx, filter, update, options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&state)
	if err != nil {
		if mongo.IsDuplicateKeyError(err) || err == mongo.ErrNoDocuments {
			return nil, errors.New("another journey transfer build is active")
		}
		return nil, fmt.Errorf("acquire transfer build lease: %w", err)
	}
	lease := &transferBuildLease{db: database.Instance.Database, token: token, heartbeatDone: make(chan struct{})}
	heartbeatCtx, stopHeartbeat := context.WithCancel(context.WithoutCancel(ctx))
	lease.stopHeartbeat = stopHeartbeat
	go lease.heartbeat(heartbeatCtx)
	return lease, nil
}

func (l *transferBuildLease) heartbeat(ctx context.Context) {
	defer close(l.heartbeatDone)
	ticker := time.NewTicker(transferBuildHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_ = l.Renew(renewCtx)
			cancel()
		}
	}
}

// WithBatch fences callback mutations against expired or superseded workers.
// Mongo transactions are required so lease validation and output writes share
// one atomic commit.
func (l *transferBuildLease) WithBatch(ctx context.Context, callback func(context.Context) error) error {
	if l == nil || l.db == nil || callback == nil {
		return errors.New("transfer build lease and callback are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	session, err := l.db.Client().StartSession()
	if err != nil {
		return fmt.Errorf("start transfer build transaction: %w", err)
	}
	defer session.EndSession(ctx)
	_, err = session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		now := time.Now()
		result, err := l.db.Collection(transferBuildStateCollection).UpdateOne(sc,
			bson.M{"_id": transferBuildLeaseID, "token": l.token, "expires_at": bson.M{"$gt": now}},
			bson.M{"$set": bson.M{"expires_at": now.Add(transferBuildLeaseDuration)}})
		if err != nil {
			return nil, fmt.Errorf("fence transfer build batch: %w", err)
		}
		if result.MatchedCount != 1 {
			return nil, errors.New("transfer build lease expired or was superseded")
		}
		if err := callback(sc); err != nil {
			return nil, err
		}
		return nil, nil
	})
	return err
}

// Renew extends the lease during long input scans or edge calculations. A
// failed renewal means this worker has lost ownership and must stop.
func (l *transferBuildLease) Renew(ctx context.Context) error {
	if l == nil || l.db == nil {
		return errors.New("transfer build lease is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	result, err := l.db.Collection(transferBuildStateCollection).UpdateOne(ctx,
		bson.M{"_id": transferBuildLeaseID, "token": l.token, "expires_at": bson.M{"$gt": now}},
		bson.M{"$set": bson.M{"expires_at": now.Add(transferBuildLeaseDuration)}})
	if err != nil {
		return fmt.Errorf("renew transfer build lease: %w", err)
	}
	if result.MatchedCount != 1 {
		return errors.New("transfer build lease expired or was superseded")
	}
	return nil
}

func (l *transferBuildLease) LoadSnapshots(ctx context.Context) (map[string]transferSnapshot, error) {
	result := make(map[string]transferSnapshot)
	cur, err := l.db.Collection(transferSnapshotCollection).Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("read transfer snapshots: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var snapshot transferSnapshot
		if err := cur.Decode(&snapshot); err != nil {
			return nil, fmt.Errorf("decode transfer snapshot: %w", err)
		}
		result[snapshot.ID] = snapshot
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("read transfer snapshots: %w", err)
	}
	return result, nil
}

func (l *transferBuildLease) Pending(ctx context.Context) (map[string]bool, error) {
	result := make(map[string]bool)
	cur, err := l.db.Collection(transferDirtyCollection).Find(ctx, bson.M{})
	if err != nil {
		return nil, fmt.Errorf("read dirty transfer origins: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return nil, err
		}
		result[row.ID] = true
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("read dirty transfer origins: %w", err)
	}
	return result, nil
}

// MarkDirty persists origins before source journey writes begin. The separate
// durable collection makes partial source writes replayable after a crash.
func (l *transferBuildLease) MarkDirty(ctx context.Context, origins []string) error {
	if len(origins) == 0 {
		return nil
	}
	rows := dirtyOriginRows(origins)
	if len(rows) == 0 {
		return nil
	}
	for start := 0; start < len(rows); start += transferDirtyBatchSize {
		end := min(start+transferDirtyBatchSize, len(rows))
		batch := rows[start:end]
		if err := l.WithBatch(ctx, func(batchCtx context.Context) error {
			_, err := l.db.Collection(transferDirtyCollection).BulkWrite(batchCtx, dirtyUpserts(batch), options.BulkWrite().SetOrdered(false))
			if err != nil {
				return fmt.Errorf("persist dirty transfer origins: %w", err)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}

func dirtyOriginRows(origins []string) []interface{} {
	rows := make([]interface{}, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, id := range origins {
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		rows = append(rows, bson.M{"_id": id})
		seen[id] = struct{}{}
	}
	return rows
}

func dirtyUpserts(rows []interface{}) []mongo.WriteModel {
	models := make([]mongo.WriteModel, 0, len(rows))
	for _, value := range rows {
		id := value.(bson.M)["_id"]
		models = append(models, mongo.NewUpdateOneModel().SetFilter(bson.M{"_id": id}).SetUpdate(bson.M{"$setOnInsert": bson.M{"created_at": time.Now()}}).SetUpsert(true))
	}
	return models
}

// SaveSnapshots performs changed-only replacements/upserts and removals. Call
// it only from WithBatch so the lease fence and data commit are atomic.
func (l *transferBuildLease) SaveSnapshots(ctx context.Context, snapshots []transferSnapshot, removed []string) error {
	coll := l.db.Collection(transferSnapshotCollection)
	models := make([]mongo.WriteModel, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot.ID == "" {
			return errors.New("transfer snapshot ID is required")
		}
		models = append(models, mongo.NewReplaceOneModel().SetFilter(bson.M{"_id": snapshot.ID}).SetReplacement(snapshot).SetUpsert(true))
	}
	if len(models) > 0 {
		if _, err := coll.BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
			return fmt.Errorf("save transfer snapshots: %w", err)
		}
	}
	if len(removed) > 0 {
		ids := make([]interface{}, 0, len(removed))
		for _, id := range removed {
			if id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			if _, err := coll.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}}); err != nil {
				return fmt.Errorf("remove transfer snapshots: %w", err)
			}
		}
	}
	return nil
}

func (l *transferBuildLease) ClearDirty(ctx context.Context, origins []string) error {
	if len(origins) == 0 {
		return nil
	}
	ids := make([]interface{}, 0, len(origins))
	for _, id := range origins {
		if id != "" {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	if _, err := l.db.Collection(transferDirtyCollection).DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}}); err != nil {
		return fmt.Errorf("clear dirty transfer origins: %w", err)
	}
	return nil
}

func (l *transferBuildLease) Release(ctx context.Context) error {
	if l == nil || l.db == nil {
		return nil
	}
	if l.stopHeartbeat != nil {
		l.stopHeartbeat()
		<-l.heartbeatDone
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err := l.db.Collection(transferBuildStateCollection).UpdateOne(ctx,
		bson.M{"_id": transferBuildLeaseID, "token": l.token},
		bson.M{"$set": bson.M{"expires_at": time.Now()}, "$unset": bson.M{"token": ""}})
	if err != nil {
		return fmt.Errorf("release transfer build lease: %w", err)
	}
	return nil
}
