package stopservices

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// BuildOptions bounds client-side memory while copying grouped references.
type BuildOptions struct {
	BatchSize int
}

type aggregateGroup struct {
	ID struct {
		Dataset    string `bson:"dataset"`
		StopRef    string `bson:"stopref"`
		ServiceRef string `bson:"serviceref"`
	} `bson:"_id"`
}

type heldBuildLease struct {
	ctx     context.Context
	cancel  context.CancelFunc
	token   string
	done    chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	lostErr error
}

// Build performs the explicit initial backfill or a full static rebuild. It
// stages one generation, then atomically swaps all static dataset pointers.
func Build(ctx context.Context, db *mongo.Database) error {
	return BuildWithOptions(ctx, db, BuildOptions{})
}

func BuildWithOptions(ctx context.Context, db *mongo.Database, buildOptions BuildOptions) (resultErr error) {
	startedAt := time.Now()
	if db == nil {
		return errors.New("stop-service database is nil")
	}
	buildOptions = normalizeBuildOptions(buildOptions)
	if err := ensureStateDocument(ctx, db); err != nil {
		return err
	}
	lease, err := acquireBuildLease(ctx, db)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lease.release(db))
	}()

	// Register legacy sources before taking the revision snapshot. A concurrent
	// publisher changes eventRevision, so the final compare-and-swap will fail.
	beforeDiscovery, err := readState(lease.ctx, db)
	if err != nil {
		return err
	}
	if beforeDiscovery.FullActiveWriters > 0 {
		return ErrSourceBusy
	}
	for _, source := range beforeDiscovery.Sources {
		if source.Dataset != "" && !excluded(source.Dataset) && source.ActiveWriters > 0 {
			return fmt.Errorf("%w: %s", ErrSourceBusy, source.Dataset)
		}
	}
	datasets, err := staticJourneyDatasets(lease.ctx, db)
	if err != nil {
		return err
	}
	for _, dataset := range datasets {
		if err := registerDataset(lease.ctx, db, dataset); err != nil {
			return fmt.Errorf("register journey dataset %q: %w", dataset, err)
		}
	}

	state, err := readState(lease.ctx, db)
	if err != nil {
		return err
	}
	if !stableAfterDiscovery(beforeDiscovery, state, newStaticDatasetRegistrations(beforeDiscovery, datasets)) {
		return ErrSourceChanged
	}
	if state.FullActiveWriters > 0 {
		return ErrSourceBusy
	}
	for _, source := range state.Sources {
		if source.Dataset != "" && !excluded(source.Dataset) && source.ActiveWriters > 0 {
			return fmt.Errorf("%w: %s", ErrSourceBusy, source.Dataset)
		}
	}

	generation := generationID()
	pairCount, err := writeGeneration(lease.ctx, db, generation, "", buildOptions.BatchSize)
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	newSources := make(map[string]datasetSourceState, len(state.Sources))
	for key, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) {
			newSources[key] = source
			continue
		}
		if source.Generation != "" {
			state.Retired = append(state.Retired, retiredGeneration{
				Dataset: source.Dataset, Generation: source.Generation, RetiredAt: now,
			})
		}
		source.Generation = generation
		source.PublishedRevision = source.Revision
		newSources[key] = source
	}

	filter := bson.M{
		"_id":               stateDocumentID,
		"eventRevision":     state.EventRevision,
		"fullRevision":      state.FullRevision,
		"fullActiveWriters": int64(0),
		"catalogRevision":   state.CatalogRevision,
		"lease.token":       lease.token,
		"lease.expiresAt":   bson.M{"$gt": time.Now().UTC()},
	}
	for key, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) {
			continue
		}
		filter["sources."+key+".revision"] = source.Revision
		filter["sources."+key+".activeWriters"] = int64(0)
	}
	update := bson.M{"$set": bson.M{
		"initialized":           true,
		"fullPublishedRevision": state.FullRevision,
		"sources":               newSources,
		"retired":               state.Retired,
	}}
	published, err := db.Collection(StateCollectionName).UpdateOne(lease.ctx, filter, update)
	if err != nil {
		return fmt.Errorf("publish full stop-service generation: %w", err)
	}
	if published.MatchedCount != 1 {
		if err := lease.lost(); err != nil {
			return err
		}
		return ErrSourceChanged
	}
	log.Info().Str("scope", "full").Str("generation", generation).
		Int64("stop_service_pairs", pairCount).Dur("duration", time.Since(startedAt)).
		Msg("Published stop-service generation")
	return pruneExpiredGenerations(lease.ctx, db, lease.token)
}

func newStaticDatasetRegistrations(state stateDocument, datasets []string) int64 {
	var registrations int64
	for _, dataset := range datasets {
		if excluded(dataset) {
			continue
		}
		if _, exists := state.Sources[sourceKey(dataset)]; !exists {
			registrations++
		}
	}
	return registrations
}

func stableAfterDiscovery(before, after stateDocument, registrations int64) bool {
	return after.EventRevision == before.EventRevision+registrations &&
		after.CatalogRevision == before.CatalogRevision+registrations &&
		after.FullRevision == before.FullRevision &&
		after.FullActiveWriters == 0
}

// BuildDataset atomically replaces one static dataset contribution. An empty
// dataset publishes an empty generation and removes only its old contribution.
func BuildDataset(ctx context.Context, db *mongo.Database, dataset string) error {
	return BuildDatasetWithOptions(ctx, db, dataset, BuildOptions{})
}

func BuildDatasetWithOptions(ctx context.Context, db *mongo.Database, dataset string, buildOptions BuildOptions) (resultErr error) {
	startedAt := time.Now()
	if db == nil {
		return errors.New("stop-service database is nil")
	}
	if dataset == "" {
		return errors.New("stop-service dataset ID is empty")
	}
	if excluded(dataset) {
		return ErrDynamicDataset
	}
	buildOptions = normalizeBuildOptions(buildOptions)
	if err := ensureStateDocument(ctx, db); err != nil {
		return err
	}
	if err := registerDataset(ctx, db, dataset); err != nil {
		return fmt.Errorf("register journey dataset %q: %w", dataset, err)
	}
	lease, err := acquireBuildLease(ctx, db)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, lease.release(db))
	}()

	state, err := readState(lease.ctx, db)
	if err != nil {
		return err
	}
	key := sourceKey(dataset)
	source, ok := state.Sources[key]
	if !ok || source.Dataset != dataset {
		return fmt.Errorf("stop-service source state is missing for dataset %q", dataset)
	}
	if state.FullActiveWriters > 0 || source.ActiveWriters > 0 {
		return fmt.Errorf("%w: %s", ErrSourceBusy, dataset)
	}
	if state.FullRevision != state.FullPublishedRevision {
		return ErrSourceChanged
	}

	generation := generationID()
	pairCount, err := writeGeneration(lease.ctx, db, generation, dataset, buildOptions.BatchSize)
	if err != nil {
		return err
	}
	filter := bson.M{
		"_id":                               stateDocumentID,
		"fullRevision":                      state.FullRevision,
		"fullActiveWriters":                 int64(0),
		"sources." + key + ".dataset":       dataset,
		"sources." + key + ".revision":      source.Revision,
		"sources." + key + ".activeWriters": int64(0),
		"lease.token":                       lease.token,
		"lease.expiresAt":                   bson.M{"$gt": time.Now().UTC()},
	}
	update := bson.M{"$set": bson.M{
		"sources." + key + ".generation":        generation,
		"sources." + key + ".publishedRevision": source.Revision,
	}}
	if source.Generation != "" {
		update["$push"] = bson.M{"retired": retiredGeneration{
			Dataset: dataset, Generation: source.Generation, RetiredAt: time.Now().UTC(),
		}}
	}
	published, err := db.Collection(StateCollectionName).UpdateOne(lease.ctx, filter, update)
	if err != nil {
		return fmt.Errorf("publish stop-service generation for %q: %w", dataset, err)
	}
	if published.MatchedCount != 1 {
		if err := lease.lost(); err != nil {
			return err
		}
		return ErrSourceChanged
	}
	log.Info().Str("scope", "dataset").Str("dataset", dataset).Str("generation", generation).
		Int64("stop_service_pairs", pairCount).Dur("duration", time.Since(startedAt)).
		Msg("Published stop-service generation")
	return pruneExpiredGenerations(lease.ctx, db, lease.token)
}

// BuildPending refreshes only dirty static datasets. It never creates the
// initial materialization implicitly.
func BuildPending(ctx context.Context, db *mongo.Database) (int, error) {
	if db == nil {
		return 0, errors.New("stop-service database is nil")
	}
	state, err := readState(ctx, db)
	if err != nil {
		return 0, err
	}
	if !state.Initialized {
		log.Info().Msg("Skipping pending stop-service refresh before explicit initial build")
		return 0, nil
	}
	if fullPending, count, err := publishFullIfPending(ctx, db); err != nil {
		return 0, err
	} else if fullPending {
		return count, nil
	}

	pending := make([]string, 0)
	for _, source := range state.Sources {
		if source.Dataset == "" || excluded(source.Dataset) ||
			source.Revision == source.PublishedRevision || source.ActiveWriters > 0 {
			continue
		}
		pending = append(pending, source.Dataset)
	}
	sort.Strings(pending)
	built := 0
	var errs []error
	for _, dataset := range pending {
		if fullPending, count, err := publishFullIfPending(ctx, db); err != nil {
			return built, errors.Join(append(errs, err)...)
		} else if fullPending {
			return count, nil
		}
		if err := BuildDataset(ctx, db, dataset); err != nil {
			if errors.Is(err, ErrSourceBusy) {
				continue
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return built, errors.Join(append(errs, fmt.Errorf("refresh stop-services dataset %q: %w", dataset, err))...)
			}
			if errors.Is(err, ErrSourceChanged) {
				if fullPending, count, fullErr := publishFullIfPending(ctx, db); fullErr != nil {
					return built, errors.Join(append(errs, fullErr)...)
				} else if fullPending {
					return count, nil
				}
			}
			errs = append(errs, fmt.Errorf("refresh stop-services dataset %q: %w", dataset, err))
			continue
		}
		built++
	}
	if fullPending, count, err := publishFullIfPending(ctx, db); err != nil {
		return built, errors.Join(append(errs, err)...)
	} else if fullPending {
		return count, nil
	}
	return built, errors.Join(errs...)
}

func publishFullIfPending(ctx context.Context, db *mongo.Database) (bool, int, error) {
	state, err := readState(ctx, db)
	if err != nil {
		return false, 0, err
	}
	if state.FullActiveWriters > 0 {
		return false, 0, ErrSourceBusy
	}
	if state.FullRevision == state.FullPublishedRevision {
		return false, 0, nil
	}
	count := 0
	for _, source := range state.Sources {
		if source.Dataset != "" && !excluded(source.Dataset) {
			count++
		}
	}
	if err := Build(ctx, db); err != nil {
		return false, 0, err
	}
	return true, count, nil
}

func normalizeBuildOptions(options BuildOptions) BuildOptions {
	if options.BatchSize <= 0 {
		options.BatchSize = DefaultBatchSize
	}
	return options
}

func staticJourneyDatasets(ctx context.Context, db *mongo.Database) ([]string, error) {
	filter := bson.M{"datasource.datasetid": bson.M{
		"$type": "string", "$ne": "",
		"$nin": bson.A{"gb-networkrail-vstp", "gb-nationalrail-darwin-amendment"},
	}}
	values, err := db.Collection("journeys").Distinct(ctx, "datasource.datasetid", filter)
	if err != nil {
		return nil, fmt.Errorf("list static journey datasets: %w", err)
	}
	datasets := make([]string, 0, len(values))
	for _, value := range values {
		dataset, ok := value.(string)
		if !ok || dataset == "" {
			return nil, fmt.Errorf("journey source dataset ID has unexpected type %T", value)
		}
		datasets = append(datasets, dataset)
	}
	sort.Strings(datasets)
	return datasets, nil
}

func writeGeneration(ctx context.Context, db *mongo.Database, generation, datasetFilter string, batchSize int) (int64, error) {
	cursor, err := db.Collection("journeys").Aggregate(ctx, stopServiceAggregation(datasetFilter),
		options.Aggregate().SetAllowDiskUse(true).SetBatchSize(int32(batchSize)))
	if err != nil {
		return 0, fmt.Errorf("aggregate stop-service references: %w", err)
	}
	defer cursor.Close(ctx)

	var pairCount int64
	rows := make([]mongo.WriteModel, 0, batchSize)
	for cursor.Next(ctx) {
		var group aggregateGroup
		if err := cursor.Decode(&group); err != nil {
			return pairCount, fmt.Errorf("decode stop-service aggregation row: %w", err)
		}
		ref := group.ID
		if ref.Dataset == "" {
			return pairCount, errors.New("journey with a service and path has no datasource.datasetid")
		}
		if excluded(ref.Dataset) {
			continue
		}
		if datasetFilter != "" && ref.Dataset != datasetFilter {
			return pairCount, fmt.Errorf("aggregation returned dataset %q while building %q", ref.Dataset, datasetFilter)
		}
		if ref.StopRef == "" || ref.ServiceRef == "" {
			continue
		}
		rows = append(rows, mongo.NewInsertOneModel().SetDocument(bson.M{
			"dataset": ref.Dataset, "generation": generation,
			"stopref": ref.StopRef, "serviceref": ref.ServiceRef,
			"createdAt": time.Now().UTC(),
		}))
		pairCount++
		if len(rows) >= batchSize {
			if err := insertBatch(ctx, db, rows); err != nil {
				return pairCount, err
			}
			rows = rows[:0]
		}
	}
	if err := cursor.Err(); err != nil {
		return pairCount, fmt.Errorf("iterate stop-service aggregation: %w", err)
	}
	if len(rows) > 0 {
		if err := insertBatch(ctx, db, rows); err != nil {
			return pairCount, err
		}
	}
	return pairCount, nil
}

func stopServiceAggregation(dataset string) mongo.Pipeline {
	filter := bson.M{
		"serviceref": bson.M{"$type": "string", "$ne": ""},
		"datasource.datasetid": bson.M{"$nin": bson.A{
			"gb-networkrail-vstp", "gb-nationalrail-darwin-amendment",
		}},
	}
	if dataset != "" {
		filter["datasource.datasetid"] = dataset
	}
	return mongo.Pipeline{
		{{Key: "$match", Value: filter}},
		{{Key: "$unwind", Value: "$path"}},
		{{Key: "$project", Value: bson.M{
			"dataset":    "$datasource.datasetid",
			"serviceRef": "$serviceref",
			"stopRefs":   bson.A{"$path.originstopref", "$path.destinationstopref"},
		}}},
		{{Key: "$unwind", Value: "$stopRefs"}},
		{{Key: "$match", Value: bson.M{"stopRefs": bson.M{"$type": "string", "$ne": ""}}}},
		{{Key: "$group", Value: bson.M{"_id": bson.M{
			"dataset": "$dataset", "stopref": "$stopRefs", "serviceref": "$serviceRef",
		}}}},
	}
}

func insertBatch(ctx context.Context, db *mongo.Database, models []mongo.WriteModel) error {
	if _, err := db.Collection(RefsCollectionName).BulkWrite(ctx, models, options.BulkWrite().SetOrdered(false)); err != nil {
		return fmt.Errorf("write stop-service batch: %w", err)
	}
	return nil
}

func acquireBuildLease(ctx context.Context, db *mongo.Database) (*heldBuildLease, error) {
	token := generationID()
	now := time.Now().UTC()
	filter := bson.M{
		"_id": stateDocumentID,
		"$or": bson.A{
			bson.M{"lease": bson.M{"$exists": false}},
			bson.M{"lease.expiresAt": bson.M{"$lte": now}},
		},
	}
	result, err := db.Collection(StateCollectionName).UpdateOne(ctx, filter, bson.M{
		"$set": bson.M{"lease": buildLease{Token: token, ExpiresAt: now.Add(buildLeaseDuration)}},
	})
	if err != nil {
		return nil, fmt.Errorf("acquire stop-service build lease: %w", err)
	}
	if result.MatchedCount != 1 {
		return nil, ErrBuildInProgress
	}
	workCtx, cancel := context.WithCancel(ctx)
	lease := &heldBuildLease{ctx: workCtx, cancel: cancel, token: token, done: make(chan struct{})}
	lease.wg.Add(1)
	go lease.renew(db)
	return lease, nil
}

func (l *heldBuildLease) renew(db *mongo.Database) {
	defer l.wg.Done()
	ticker := time.NewTicker(buildLeaseDuration / 3)
	defer ticker.Stop()
	for {
		select {
		case <-l.done:
			return
		case <-l.ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().UTC()
			ctx, cancel := context.WithTimeout(l.ctx, 20*time.Second)
			result, err := db.Collection(StateCollectionName).UpdateOne(ctx,
				bson.M{"_id": stateDocumentID, "lease.token": l.token, "lease.expiresAt": bson.M{"$gt": now}},
				bson.M{"$set": bson.M{"lease.expiresAt": now.Add(buildLeaseDuration)}},
			)
			cancel()
			if err != nil || result.MatchedCount != 1 {
				l.mu.Lock()
				if err != nil {
					l.lostErr = fmt.Errorf("%w: renew: %v", ErrBuildLeaseLost, err)
				} else {
					l.lostErr = ErrBuildLeaseLost
				}
				l.mu.Unlock()
				l.cancel()
				return
			}
		}
	}
}

func (l *heldBuildLease) lost() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lostErr
}

func (l *heldBuildLease) release(db *mongo.Database) error {
	close(l.done)
	l.wg.Wait()
	l.cancel()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(l.ctx), 10*time.Second)
	defer cancel()
	result, err := db.Collection(StateCollectionName).UpdateOne(ctx,
		bson.M{"_id": stateDocumentID, "lease.token": l.token},
		bson.M{"$unset": bson.M{"lease": ""}},
	)
	if err != nil {
		return errors.Join(l.lost(), fmt.Errorf("release stop-service build lease: %w", err))
	}
	if result.MatchedCount != 1 {
		return l.lost()
	}
	return l.lost()
}

func pruneExpiredGenerations(ctx context.Context, db *mongo.Database, token string) error {
	state, err := readState(ctx, db)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	if state.Lease == nil || state.Lease.Token != token || !state.Lease.ExpiresAt.After(now) {
		return ErrBuildLeaseLost
	}
	leaseResult, err := db.Collection(StateCollectionName).UpdateOne(ctx,
		bson.M{"_id": stateDocumentID, "lease.token": token, "lease.expiresAt": bson.M{"$gt": now}},
		bson.M{"$set": bson.M{"lease.expiresAt": now.Add(buildLeaseDuration)}},
	)
	if err != nil {
		return fmt.Errorf("renew stop-service build lease before cleanup: %w", err)
	}
	if leaseResult.MatchedCount != 1 {
		return ErrBuildLeaseLost
	}
	cutoff := now.Add(-GenerationGrace)
	protected := make(bson.A, 0, len(state.Sources)+len(state.Retired))
	for _, source := range state.Sources {
		if source.Dataset != "" && source.Generation != "" {
			protected = append(protected, bson.M{"dataset": source.Dataset, "generation": source.Generation})
		}
	}
	for _, retired := range state.Retired {
		if retired.RetiredAt.After(cutoff) {
			protected = append(protected, bson.M{"dataset": retired.Dataset, "generation": retired.Generation})
		}
	}
	filter := bson.M{"createdAt": bson.M{"$lt": cutoff}}
	if len(protected) > 0 {
		filter["$nor"] = protected
	}
	if _, err := db.Collection(RefsCollectionName).DeleteMany(ctx, filter); err != nil {
		return fmt.Errorf("prune retired stop-service generations: %w", err)
	}
	metadataResult, err := db.Collection(StateCollectionName).UpdateOne(ctx,
		bson.M{"_id": stateDocumentID, "lease.token": token, "lease.expiresAt": bson.M{"$gt": time.Now().UTC()}},
		bson.M{"$pull": bson.M{"retired": bson.M{"retiredAt": bson.M{"$lte": cutoff}}}},
	)
	if err != nil {
		return fmt.Errorf("prune retired stop-service metadata: %w", err)
	}
	if metadataResult.MatchedCount != 1 {
		return ErrBuildLeaseLost
	}
	return nil
}
