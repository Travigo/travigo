package stopservices

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	StateCollectionName = "stop_service_state"
	RefsCollectionName  = "stop_service_refs"
	stateDocumentID     = "static"
	DefaultBatchSize    = 1000
	GenerationGrace     = 24 * time.Hour
	buildLeaseDuration  = 30 * time.Minute
)

var (
	// ErrUnbuilt means the explicit initial materialization has not completed.
	ErrUnbuilt         = errors.New("stop-service materialization has not been built")
	ErrSourceBusy      = errors.New("stop-service source dataset is being published")
	ErrSourceChanged   = errors.New("stop-service source changed during materialization")
	ErrBuildInProgress = errors.New("another stop-service build is in progress")
	ErrBuildLeaseLost  = errors.New("stop-service build lease was lost")
	ErrDynamicDataset  = errors.New("dynamic journey dataset is not included in static stop-service materialization")
)

// Dynamic journey datasets are short-lived overlays and are intentionally
// served by the realtime path rather than this static, durable index.
var excludedDatasets = map[string]struct{}{
	"gb-networkrail-vstp":              {},
	"gb-nationalrail-darwin-amendment": {},
}

type datasetSourceState struct {
	Dataset           string `bson:"dataset"`
	Revision          int64  `bson:"revision"`
	ActiveWriters     int64  `bson:"activeWriters"`
	PublishedRevision int64  `bson:"publishedRevision"`
	Generation        string `bson:"generation,omitempty"`
}

type retiredGeneration struct {
	Dataset    string    `bson:"dataset"`
	Generation string    `bson:"generation"`
	RetiredAt  time.Time `bson:"retiredAt"`
}

type buildLease struct {
	Token     string    `bson:"token"`
	ExpiresAt time.Time `bson:"expiresAt"`
}

type stateDocument struct {
	ID                    string                        `bson:"_id"`
	Initialized           bool                          `bson:"initialized"`
	EventRevision         int64                         `bson:"eventRevision"`
	FullRevision          int64                         `bson:"fullRevision"`
	FullPublishedRevision int64                         `bson:"fullPublishedRevision"`
	FullActiveWriters     int64                         `bson:"fullActiveWriters"`
	CatalogRevision       int64                         `bson:"catalogRevision"`
	Sources               map[string]datasetSourceState `bson:"sources"`
	Retired               []retiredGeneration           `bson:"retired"`
	Lease                 *buildLease                   `bson:"lease,omitempty"`
}

func sourceKey(dataset string) string {
	sum := sha256.Sum256([]byte(dataset))
	return hex.EncodeToString(sum[:])
}

func excluded(dataset string) bool {
	_, ok := excludedDatasets[dataset]
	return ok
}

// EnsureSchema creates the indexes and the singleton publication document.
// It is idempotent; source-write registration uses only the cheap state upsert.
func EnsureSchema(ctx context.Context, db *mongo.Database) error {
	if db == nil {
		return errors.New("stop-service database is nil")
	}
	if err := ensureStateDocument(ctx, db); err != nil {
		return err
	}

	_, err := db.Collection(RefsCollectionName).Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys: bson.D{
				{Key: "dataset", Value: 1},
				{Key: "generation", Value: 1},
				{Key: "stopref", Value: 1},
				{Key: "serviceref", Value: 1},
			},
			Options: options.Index().SetUnique(true).SetName("dataset_generation_stop_service_unique"),
		},
		{
			Keys: bson.D{
				{Key: "stopref", Value: 1},
				{Key: "dataset", Value: 1},
				{Key: "generation", Value: 1},
			},
			Options: options.Index().SetName("stop_dataset_generation"),
		},
		{
			Keys:    bson.D{{Key: "createdAt", Value: 1}},
			Options: options.Index().SetName("created_at"),
		},
	})
	if err != nil {
		return fmt.Errorf("create stop-service indexes: %w", err)
	}
	return nil
}

func ensureStateDocument(ctx context.Context, db *mongo.Database) error {
	_, err := db.Collection(StateCollectionName).UpdateOne(ctx, bson.M{"_id": stateDocumentID}, bson.M{
		"$setOnInsert": bson.M{
			"initialized":           false,
			"eventRevision":         int64(0),
			"fullRevision":          int64(0),
			"fullPublishedRevision": int64(0),
			"fullActiveWriters":     int64(0),
			"catalogRevision":       int64(0),
			"sources":               bson.M{},
			"retired":               bson.A{},
		},
	}, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("initialize stop-service state: %w", err)
	}
	return nil
}

func readState(ctx context.Context, db *mongo.Database) (stateDocument, error) {
	var state stateDocument
	err := db.Collection(StateCollectionName).FindOne(ctx, bson.M{"_id": stateDocumentID}).Decode(&state)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return state, ErrUnbuilt
	}
	if err != nil {
		return state, err
	}
	if state.Sources == nil {
		state.Sources = map[string]datasetSourceState{}
	}
	return state, nil
}

func generationID() string { return primitive.NewObjectID().Hex() }
