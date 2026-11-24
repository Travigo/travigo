package datalinker

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const transferReadBatch = 512

// Only values that affect the generated graph participate in its fingerprint.
func transferRules(config StopTransferBuildConfig) string {
	return fmt.Sprintf("v1/%d/%d/%g/%d/%d", config.MaxDistanceMetres, config.MinChangeSeconds, config.WalkSpeedMetresPerSec, config.MaxNearbyTransfersPerStop, config.SameStopGroupMultiplier)
}

type transferBuildCheckpoint struct {
	FullRepair      bool              `bson:"fullrepair"`
	Rules           string            `bson:"transferrules"`
	Linked          string            `bson:"linkedrevision"`
	Services        map[string]string `bson:"servicegenerations"`
	ServiceRevision int64             `bson:"stopservicesrevision"`
	FilterActive    bool              `bson:"servicefilteractive"`
	RebuildAfter    time.Time         `bson:"rebuildafter"`
}

func BuildStopTransfers(config StopTransferBuildConfig) (err error) {
	ctx := context.Background()
	db := database.Instance.Database
	// Standalone servers do not support the transaction used to fence a writer
	// against a replacement worker. Keep the existing full-build path there.
	var hello struct {
		SetName string `bson:"setName"`
		Msg     string `bson:"msg"`
	}
	if err = db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return err
	}
	if hello.SetName == "" && hello.Msg != "isdbgrid" {
		return buildStandaloneStopTransfers(config)
	}
	lease, err := acquireTransferBuild(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lease.Release(ctx)) }()
	return buildIncrementalTransfers(ctx, lease, config.withDefaults(), stopProvidingDatasetIDs())
}

func buildIncrementalTransfers(ctx context.Context, lease *transferBuildLease, config StopTransferBuildConfig, sourceIDs []string) error {
	start := time.Now()
	versions := database.GetCollection("dataset_versions")
	filter := bson.M{"dataset": datasetversion.StopTransfersDataset}
	var cp transferBuildCheckpoint
	if err := versions.FindOne(ctx, filter).Decode(&cp); err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return err
	}
	changedSources, err := datasetversion.ChangedDatasets(ctx, datasetversion.StopTransfersDataset, sourceIDs)
	if err != nil {
		return err
	}
	if err = ensureStopLinksCurrent(ctx, changedSources); err != nil {
		return err
	}
	if !cp.RebuildAfter.IsZero() {
		var linked ctdf.DatasetVersion
		if err = versions.FindOne(ctx, bson.M{"dataset": datasetversion.LinkerDataset("stop")}).Decode(&linked); err != nil {
			return err
		}
		if !linked.LastModified.After(cp.RebuildAfter) {
			return errors.New("link stops after dataset retirement before rebuilding stop transfers")
		}
	}
	ids, linkRevision, full, err := ReadLinkedStopChanges(ctx, cp.Linked)
	if err != nil {
		return err
	}
	serviceRevision, current, err := stopservices.CurrentRevision(ctx, lease.db)
	if err != nil {
		return err
	}
	if !current {
		log.Info().Msg("Deferring stop transfers until stop services are current")
		return nil
	}
	pending, err := lease.Pending(ctx)
	if err != nil {
		return err
	}
	rules := transferRules(config)
	bootstrap := cp.Rules == ""
	rebuildAll := rules != cp.Rules || cp.FullRepair
	// A pre-journal linker can only be consumed through a full snapshot diff.
	if linkRevision == "" && cp.Linked == "" && !bootstrap && len(changedSources) == 0 && cp.RebuildAfter.IsZero() {
		full = false
	}
	full = full || rebuildAll || len(pending) > 0
	membership := cp.Services
	if full {
		membership = nil
	}
	var allRefs map[string]struct{}
	var refChanges map[string]bool
	active := cp.FilterActive
	if full || serviceRevision != cp.ServiceRevision {
		allRefs, membership, refChanges, active, serviceRevision, err = stopservices.TransferMembership(ctx, lease.db, membership)
		if err != nil {
			return err
		}
		full = full || allRefs != nil || active != cp.FilterActive
	}
	if !full && len(ids) == 0 && len(refChanges) == 0 && serviceRevision == cp.ServiceRevision && linkRevision == cp.Linked && len(changedSources) == 0 && cp.RebuildAfter.IsZero() {
		return nil
	}
	if full && allRefs == nil {
		allRefs, membership, _, active, serviceRevision, err = stopservices.TransferMembership(ctx, lease.db, nil)
		if err != nil {
			return err
		}
	}
	inputsStarted := time.Now()
	if bootstrap {
		if err = ensureTransferLocalityIndexes(ctx); err != nil {
			return err
		}
	}
	old := make(map[string]transferSnapshot)
	fresh, err := readTransferInputs(ctx, full, ids, refChanges, active, allRefs)
	if err != nil {
		return err
	}
	if full {
		old, err = lease.LoadSnapshots(ctx)
	} else {
		keys := append(slices.Clone(ids), slices.Collect(maps.Keys(fresh))...)
		err = loadTransferSnapshotIDs(ctx, keys, old)
	}
	if err != nil {
		return err
	}
	changed := changedTransferInputs(old, fresh, ids, full)
	inputsDuration := time.Since(inputsStarted)
	planStarted := time.Now()
	ix, affected, err := planTransferOrigins(ctx, config, old, fresh, changed, pending, full, rebuildAll, bootstrap || cp.FullRepair)
	if err != nil {
		return err
	}
	origins := slices.Sorted(maps.Keys(affected))
	planDuration := time.Since(planStarted)
	// A single repair marker avoids writing and then deleting a pending row
	// for every stop on the first build. Recovery enumerates generated origins,
	// so it also finds partially written edges for stops subsequently removed.
	broadBuild := rebuildAll || len(origins) > 5000
	if broadBuild && !cp.FullRepair {
		err = lease.WithBatch(ctx, func(tx context.Context) error {
			_, err := versions.UpdateOne(tx, filter, bson.M{"$set": bson.M{"fullrepair": true}}, options.Update().SetUpsert(true))
			return err
		})
	} else if !broadBuild {
		err = lease.MarkDirty(ctx, origins)
	}
	if err != nil {
		return err
	}
	var inserted, updated, deleted int64
	var computeDuration, writeDuration time.Duration
	for batch := range slices.Chunk(origins, min(config.BatchSize, 128)) {
		computeStarted := time.Now()
		edges := ix.edges(batch)
		computeDuration += time.Since(computeStarted)
		writeStarted := time.Now()
		var i, u, d int64
		err = lease.WithBatch(ctx, func(tx context.Context) error {
			var err error
			i, u, d, err = reconcileTransferEdges(tx, batch, edges, config)
			return err
		})
		if err != nil {
			return err
		}
		writeDuration += time.Since(writeStarted)
		inserted += i
		updated += u
		deleted += d
	}
	for batch := range slices.Chunk(slices.Collect(maps.Keys(changed)), transferReadBatch) {
		var add []transferSnapshot
		var drop []string
		for _, id := range batch {
			if s, ok := fresh[id]; ok {
				add = append(add, s)
			} else {
				drop = append(drop, id)
			}
		}
		if err = lease.WithBatch(ctx, func(tx context.Context) error { return lease.SaveSnapshots(tx, add, drop) }); err != nil {
			return err
		}
	}
	// Use the observed revisions. An import that completes later remains pending.
	err = lease.WithBatch(ctx, func(tx context.Context) error {
		_, latest, _, err := ReadLinkedStopChanges(tx, linkRevision)
		if err != nil {
			return err
		}
		rev, stable, err := stopservices.CurrentRevision(tx, lease.db)
		if err != nil {
			return err
		}
		if latest != linkRevision || !stable || rev != serviceRevision {
			return errors.New("stop transfer inputs changed during generation")
		}
		checkpointFilter := maps.Clone(filter)
		if !cp.RebuildAfter.IsZero() {
			checkpointFilter["rebuildafter"] = cp.RebuildAfter
		}
		fields := bson.M{"lastmodified": start.Add(-time.Millisecond), "transferrules": rules, "linkedrevision": linkRevision, "servicegenerations": membership, "stopservicesrevision": serviceRevision, "servicefilteractive": active}
		unset := bson.M{"servicestoprefshash": "", "standalonerules": "", "fullrepair": ""}
		if !cp.RebuildAfter.IsZero() {
			unset["rebuildafter"] = ""
		}
		res, err := versions.UpdateOne(tx, checkpointFilter, bson.M{"$set": fields, "$unset": unset}, options.Update().SetUpsert(cp.RebuildAfter.IsZero()))
		if err == nil && res.MatchedCount+res.UpsertedCount != 1 {
			return errors.New("stop transfer rebuild request changed during build")
		}
		return err
	})
	if err != nil {
		return err
	}
	clearIDs := origins
	if broadBuild {
		clearIDs = slices.Collect(maps.Keys(pending))
	}
	for batch := range slices.Chunk(clearIDs, transferReadBatch) {
		if err = lease.WithBatch(ctx, func(tx context.Context) error { return lease.ClearDirty(tx, batch) }); err != nil {
			return err
		}
	}
	log.Info().
		Bool("full_input_scan", full).
		Int("changed_datasets", len(changedSources)).
		Int("live_stops_read", len(fresh)).
		Int("changed_stops", len(changed)).
		Int("affected_origins", len(origins)).
		Int64("inserted", inserted).Int64("updated", updated).Int64("deleted", deleted).
		Dur("read_inputs", inputsDuration).Dur("plan_origins", planDuration).
		Dur("generate_edges", computeDuration).Dur("reconcile_edges", writeDuration).
		Dur("duration", time.Since(start)).Msg("Reconciled generated stop transfers")
	return nil
}

func changedTransferInputs(old, fresh map[string]transferSnapshot, ids []string, full bool) map[string]bool {
	// On a delta read an absent canonical ID is a deletion only when the linker
	// reported that ID. Stops found via alias membership are always current rows.
	changed := map[string]bool{}
	for id, s := range fresh {
		if prev, ok := old[id]; !ok || !reflect.DeepEqual(prev, s) {
			changed[id] = true
		}
	}
	removed := func(id string) {
		if _, exists := fresh[id]; !exists {
			if _, was := old[id]; was {
				changed[id] = true
			}
		}
	}
	if full {
		for id := range old {
			removed(id)
		}
	} else {
		for _, id := range ids {
			removed(id)
		}
	}
	return changed
}

func planTransferOrigins(ctx context.Context, config StopTransferBuildConfig, old, fresh map[string]transferSnapshot, changed, pending map[string]bool, full, rebuildAll, bootstrap bool) (*transferIndex, map[string]bool, error) {
	affected := maps.Clone(pending)
	var ix *transferIndex
	if len(changed) == 0 && len(pending) == 0 && !rebuildAll {
		return nil, affected, nil
	}

	if !full {
		seeds := make([]transferSnapshot, 0, 2*len(changed))
		for id := range changed {
			if s, ok := old[id]; ok {
				seeds = append(seeds, s)
			}
			if s, ok := fresh[id]; ok {
				seeds = append(seeds, s)
			}
		}
		if err := loadTransferNeighbourhood(ctx, seeds, config.MaxDistanceMetres, old); err != nil {
			return nil, nil, err
		}
	}
	makeIndex := func() *transferIndex {
		rows := make([]*transferStop, 0, len(old)+len(fresh))
		for id, s := range old {
			if !changed[id] {
				rows = append(rows, s.stop(len(rows)))
			}
		}
		for id := range changed {
			if s, ok := fresh[id]; ok {
				rows = append(rows, s.stop(len(rows)))
			}
		}
		return newTransferIndex(rows, config)
	}
	ix = makeIndex()
	if rebuildAll {
		for id := range old {
			affected[id] = true
		}
		for id := range fresh {
			affected[id] = true
		}
		if bootstrap {
			if err := includeLegacyTransferOrigins(ctx, affected); err != nil {
				return nil, nil, err
			}
		}
	} else {
		maps.Copy(affected, ix.affected(old, changed))
	}
	if !full && len(affected) > 0 {
		// An affected neighbour can pick a replacement on the opposite side of its
		// radius. Fetch that second neighbourhood before selecting the nearest N.
		seeds := make([]transferSnapshot, 0, len(affected))
		for id := range affected {
			if s, ok := fresh[id]; ok {
				seeds = append(seeds, s)
			} else if s, ok := old[id]; ok {
				seeds = append(seeds, s)
			}
		}
		if err := loadTransferNeighbourhood(ctx, seeds, config.MaxDistanceMetres, old); err != nil {
			return nil, nil, err
		}
		ix = makeIndex()
	}

	return ix, affected, nil
}

// Stream origins so the migration also removes legacy edges whose origins
// disappeared before snapshots existed, without MongoDB's distinct size limit.
func includeLegacyTransferOrigins(ctx context.Context, into map[string]bool) error {
	cur, err := database.GetCollection("stop_transfers").Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"datasource.originalformat": stopTransferGeneratorFormat}}},
		{{Key: "$group", Value: bson.M{"_id": "$fromstopref"}}},
	}, options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var row struct {
			ID string `bson:"_id"`
		}
		if err := cur.Decode(&row); err != nil {
			return err
		}
		into[row.ID] = true
	}
	return cur.Err()
}

func readTransferInputs(ctx context.Context, full bool, ids []string, changedRefs map[string]bool, active bool, allRefs map[string]struct{}) (map[string]transferSnapshot, error) {
	fresh := map[string]transferSnapshot{}
	if full {
		err := scanTransferStops(ctx, nil, func(stop *ctdf.Stop) error {
			fresh[stop.PrimaryIdentifier] = snapshotForStop(transferStopFromCTDF(stop, 0, stopHasService(stop, allRefs)))
			return nil
		})
		return fresh, err
	}
	// One indexed query covers canonical changes and alias/platform service changes.
	for batch := range slices.Chunk(append(slices.Clone(ids), slices.Collect(maps.Keys(changedRefs))...), transferReadBatch) {
		in := bson.M{"$in": batch}
		match := bson.M{"$or": bson.A{bson.M{"primaryidentifier": in}, bson.M{"otheridentifiers": in}, bson.M{"platforms.primaryidentifier": in}}}
		var stops []*ctdf.Stop
		var refs []string
		err := scanTransferStops(ctx, match, func(stop *ctdf.Stop) error {
			stops = append(stops, stop)
			refs = append(refs, stop.PrimaryIdentifier)
			refs = append(refs, stop.OtherIdentifiers...)
			for _, p := range stop.Platforms {
				if p != nil {
					refs = append(refs, p.PrimaryIdentifier)
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		served := map[string]struct{}{}
		if active {
			byRef, err := stopservices.BatchForStops(ctx, database.Instance.Database, refs)
			if err != nil {
				return nil, err
			}
			for ref, services := range byRef {
				if len(services) > 0 {
					served[ref] = struct{}{}
				}
			}
		}
		for _, stop := range stops {
			hasService := !active || stopMatchesService(stop, served)
			fresh[stop.PrimaryIdentifier] = snapshotForStop(transferStopFromCTDF(stop, 0, hasService))
		}
	}
	return fresh, nil
}
