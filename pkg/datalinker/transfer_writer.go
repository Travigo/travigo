package datalinker

import (
	"context"
	"fmt"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// reconcileTransferEdges reconciles generated outgoing edges for the requested
// origins. Endpoint identity, rather than the potentially ambiguous generated
// identifier, is used for every update and delete.
func reconcileTransferEdges(ctx context.Context, origins []string, desired map[transferKey]transferCandidate, config StopTransferBuildConfig) (inserted, updated, deleted int64, err error) {
	if len(origins) == 0 {
		return 0, 0, 0, nil
	}
	collection := database.GetCollection("stop_transfers")
	query := bson.M{"datasource.originalformat": stopTransferGeneratorFormat, "fromstopref": bson.M{"$in": origins}}
	projection := bson.M{"_id": 1, "fromstopref": 1, "tostopref": 1, "type": 1, "distancemetres": 1, "walkdurationseconds": 1, "minchangedurationseconds": 1, "totaldurationseconds": 1, "generatedradiusmetres": 1}
	cursor, err := collection.Find(ctx, query, options.Find().SetProjection(projection))
	if err != nil {
		return 0, 0, 0, err
	}
	defer cursor.Close(ctx)
	type storedEdge struct {
		ID       interface{}           `bson:"_id"`
		From     string                `bson:"fromstopref"`
		To       string                `bson:"tostopref"`
		Type     ctdf.StopTransferType `bson:"type"`
		Distance int                   `bson:"distancemetres"`
		Walk     int                   `bson:"walkdurationseconds"`
		Min      int                   `bson:"minchangedurationseconds"`
		Total    int                   `bson:"totaldurationseconds"`
		Radius   int                   `bson:"generatedradiusmetres"`
	}
	existing := map[transferKey][]storedEdge{}
	for cursor.Next(ctx) {
		var edge storedEdge
		if err = cursor.Decode(&edge); err != nil {
			return 0, 0, 0, err
		}
		k := transferKey{edge.From, edge.To}
		existing[k] = append(existing[k], edge)
	}
	if err = cursor.Err(); err != nil {
		return 0, 0, 0, err
	}
	config = config.withDefaults()
	now := time.Now().Truncate(time.Millisecond)
	source := &ctdf.DataSourceReference{OriginalFormat: stopTransferGeneratorFormat, ProviderName: "Travigo", ProviderID: "travigo", DatasetID: "travigo-stop-transfers", Timestamp: now.Format(time.RFC3339)}
	keys := make(map[transferKey]struct{}, len(existing)+len(desired))
	for k := range existing {
		keys[k] = struct{}{}
	}
	originsSet := make(map[string]struct{}, len(origins))
	for _, origin := range origins {
		originsSet[origin] = struct{}{}
	}
	for k := range desired {
		if _, ok := originsSet[k.from]; ok {
			keys[k] = struct{}{}
		}
	}
	batchSize := config.BatchSize
	initialCapacity := batchSize
	if initialCapacity > 1024 {
		initialCapacity = 1024
	}
	operations := make([]mongo.WriteModel, 0, initialCapacity)
	flush := func() error {
		if len(operations) == 0 {
			return nil
		}
		unordered := false
		r, e := collection.BulkWrite(ctx, operations, &options.BulkWriteOptions{Ordered: &unordered})
		if e != nil {
			return e
		}
		inserted += r.UpsertedCount
		updated += r.ModifiedCount
		deleted += r.DeletedCount
		operations = operations[:0]
		return nil
	}
	for k := range keys {
		rows := existing[k]
		candidate, wanted := desired[k]
		filter := bson.M{"fromstopref": k.from, "tostopref": k.to, "datasource.originalformat": stopTransferGeneratorFormat}
		if !wanted {
			if len(rows) > 0 {
				operations = append(operations, mongo.NewDeleteManyModel().SetFilter(filter))
			}
			if len(operations) >= batchSize {
				if err = flush(); err != nil {
					return inserted, updated, deleted, err
				}
			}
			continue
		}
		id := fmt.Sprintf("generated-"+ctdf.StopTransferIDFormat, k.from, k.to)
		changed := len(rows) == 0
		if len(rows) > 0 {
			r := rows[0]
			changed = r.Type != candidate.transferType || r.Distance != candidate.distanceMetres || r.Walk != candidate.walkDurationSeconds || r.Min != candidate.minChangeDurationSeconds || r.Total != candidate.totalDurationSeconds || r.Radius != candidate.generatedRadiusMetres
		}
		if changed {
			set := bson.M{"primaryidentifier": id, "type": candidate.transferType, "distancemetres": candidate.distanceMetres, "walkdurationseconds": candidate.walkDurationSeconds, "minchangedurationseconds": candidate.minChangeDurationSeconds, "totaldurationseconds": candidate.totalDurationSeconds, "generatedradiusmetres": candidate.generatedRadiusMetres, "modificationdatetime": now, "datasource": source}
			update := bson.M{"$set": set}
			if len(rows) == 0 {
				set["fromstopref"] = k.from
				set["tostopref"] = k.to
				set["creationdatetime"] = now
				update = bson.M{"$setOnInsert": set}
			}
			if len(rows) > 0 {
				// Update precisely the row retained below, rather than letting Mongo
				// choose an arbitrary duplicate matching the endpoint filter.
				filter["_id"] = rows[0].ID
			}
			operations = append(operations, mongo.NewUpdateOneModel().SetFilter(filter).SetUpdate(update).SetUpsert(len(rows) == 0))
		}
		// Remove any extra generated documents with the same endpoint pair. The
		// filter remains endpoint keyed, so a colliding identifier is untouched.
		if len(rows) > 1 {
			dup := bson.M{"fromstopref": k.from, "tostopref": k.to, "datasource.originalformat": stopTransferGeneratorFormat, "_id": bson.M{"$ne": rows[0].ID}}
			operations = append(operations, mongo.NewDeleteManyModel().SetFilter(dup))
		}
		if len(operations) >= batchSize {
			if err = flush(); err != nil {
				return inserted, updated, deleted, err
			}
		}
	}
	if err = flush(); err != nil {
		return inserted, updated, deleted, err
	}
	return inserted, updated, deleted, nil
}
