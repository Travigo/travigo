package datalinker

import (
	"context"
	"errors"
	"fmt"
	"math"
	"runtime"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/dataimporter/datasets"
	"github.com/travigo/travigo/pkg/dataimporter/manager"
	"github.com/travigo/travigo/pkg/datasetversion"
	"github.com/travigo/travigo/pkg/stopservices"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultStopTransferMaxDistanceMetres        = 500
	defaultStopTransferMinChangeSeconds         = 60
	defaultStopTransferWalkSpeedMetresPerSecond = 1.3
	defaultStopTransferBatchSize                = 10000
	defaultStopTransferMaxNearbyTransfers       = 24
	stopTransferGeneratorFormat                 = "travigo-stop-transfer-generator"
	metresPerDegree                             = 111320.0
)

type StopTransferBuildConfig struct {
	MaxDistanceMetres         int
	MinChangeSeconds          int
	WalkSpeedMetresPerSec     float64
	BatchSize                 int
	MaxNearbyTransfersPerStop int
	WorkerCount               int
	SameStopGroupMultiplier   int
}

type transferStop struct {
	PrimaryIdentifier string
	Index             int
	// HasService also retains rail stops, whose dynamic VSTP journeys are not
	// represented in the static stop-service materialization.
	HasService   bool
	Location     *ctdf.Location
	Latitude     float64
	Longitude    float64
	CosLatitude  float64
	Associations []*ctdf.Association
	Platforms    []*ctdf.StopPlatform
}

type transferKey struct {
	from string
	to   string
}

type transferCandidate struct {
	key                      transferKey
	transferType             ctdf.StopTransferType
	distanceMetres           int
	walkDurationSeconds      int
	minChangeDurationSeconds int
	totalDurationSeconds     int
	generatedRadiusMetres    int
	priority                 int
}

type gridKey struct {
	lat int
	lon int
}

type nearbyTransferCandidate struct {
	toStopIndex    int
	distanceMetres int
}

type nearbyStopCandidates struct {
	stopIndex  int
	candidates []nearbyTransferCandidate
}

type nearbyDiscoveryResult struct {
	stopCandidates []nearbyStopCandidates
	candidateEdges int
}

func (config StopTransferBuildConfig) withDefaults() StopTransferBuildConfig {
	if config.MaxDistanceMetres <= 0 {
		config.MaxDistanceMetres = defaultStopTransferMaxDistanceMetres
	}
	if config.MinChangeSeconds < 0 {
		config.MinChangeSeconds = defaultStopTransferMinChangeSeconds
	}
	if config.WalkSpeedMetresPerSec <= 0 {
		config.WalkSpeedMetresPerSec = defaultStopTransferWalkSpeedMetresPerSecond
	}
	if config.BatchSize <= 0 {
		config.BatchSize = defaultStopTransferBatchSize
	}
	if config.MaxNearbyTransfersPerStop <= 0 {
		config.MaxNearbyTransfersPerStop = defaultStopTransferMaxNearbyTransfers
	}
	if config.WorkerCount <= 0 {
		config.WorkerCount = defaultStopTransferWorkerCount()
	}
	if config.SameStopGroupMultiplier <= 0 {
		config.SameStopGroupMultiplier = 2
	}

	return config
}

func BuildStopTransfers(config StopTransferBuildConfig) error {
	ctx := context.Background()
	buildStartedAt := time.Now()
	versions := database.GetCollection("dataset_versions")
	filter := bson.M{"dataset": datasetversion.StopTransfersDataset}
	var version struct {
		RebuildAfter         time.Time `bson:"rebuildafter"`
		StopServicesRevision int64     `bson:"stopservicesrevision"`
		ServiceStopRefsHash  string    `bson:"servicestoprefshash"`
	}
	err := versions.FindOne(ctx, filter).Decode(&version)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return fmt.Errorf("load stop-transfer rebuild checkpoint: %w", err)
	}
	forceRebuild := !version.RebuildAfter.IsZero()
	var changedDatasets []string
	if !forceRebuild {
		changedDatasets, err = datasetversion.ChangedDatasets(ctx, datasetversion.StopTransfersDataset, stopProvidingDatasetIDs())
		if err != nil {
			return err
		}
		if len(changedDatasets) > 0 {
			if err := ensureStopLinksCurrent(ctx, changedDatasets); err != nil {
				return err
			}
		}
	} else {
		var linked ctdf.DatasetVersion
		err := versions.FindOne(ctx, bson.M{"dataset": datasetversion.LinkerDataset("stop")}).Decode(&linked)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return fmt.Errorf("load linked stops checkpoint: %w", err)
		}
		if !linked.LastModified.After(version.RebuildAfter) {
			return fmt.Errorf("link stops after dataset retirement before rebuilding stop transfers")
		}
	}
	stopSourcesChanged := len(changedDatasets) > 0

	serviceRevision, servicesCurrent, err := stopservices.CurrentRevision(ctx, database.Instance.Database)
	if err != nil {
		return fmt.Errorf("read stop-service revision: %w", err)
	}
	if !forceRebuild && !stopSourcesChanged && !servicesCurrent {
		log.Info().Msg("Stop-service view is stale; skipping stop transfer membership check")
		return nil
	}
	if !forceRebuild && !stopSourcesChanged && servicesCurrent &&
		version.ServiceStopRefsHash != "" && serviceRevision == version.StopServicesRevision {
		log.Info().Msg("Stop transfer datasets and served-stop membership are unchanged; skipping build")
		return nil
	}

	var serviceStopRefs map[string]struct{}
	serviceStopRefsHash := version.ServiceStopRefsHash
	serviceSnapshotCurrent := false
	if servicesCurrent {
		serviceStopRefs, serviceStopRefsHash, serviceRevision, err = stopservices.CurrentStopRefs(ctx, database.Instance.Database, serviceRevision)
		if err != nil {
			return fmt.Errorf("load current served stops: %w", err)
		}
		serviceSnapshotCurrent = true
	}

	graphChanged := forceRebuild || stopSourcesChanged
	if !graphChanged && serviceSnapshotCurrent &&
		(serviceStopRefsHash != version.ServiceStopRefsHash || version.ServiceStopRefsHash == "") {
		graphChanged = true
	}
	if !graphChanged {
		if serviceSnapshotCurrent {
			if err := checkpointStopServiceMembership(ctx, versions, serviceRevision, serviceStopRefsHash); err != nil {
				return err
			}
		}
		log.Info().Msg("Stop transfer inputs are unchanged; skipping build")
		return nil
	}

	config = config.withDefaults()
	start := time.Now()

	phaseStart := time.Now()
	filterNearbyByService := len(serviceStopRefs) > 0
	stops, err := loadTransferStops(ctx, serviceStopRefs)
	if err != nil {
		return err
	}
	log.Info().Dur("duration", time.Since(phaseStart)).Msg("Stop transfer phase completed: load stops")

	// Recompute the complete generated graph whenever a source changes. A source
	// update can affect incoming edges from unchanged stops, nearest-N choices,
	// and stops removed from the live collection, so the current stop set alone
	// is not enough to safely scope an incremental delete or regeneration.
	transfers := make(map[transferKey]transferCandidate)

	phaseStart = time.Now()
	buildSameStopGroupTransfers(stops, transfers, config)
	log.Info().Dur("duration", time.Since(phaseStart)).Msg("Stop transfer phase completed: same stop groups")

	phaseStart = time.Now()
	buildPlatformAliasTransfers(stops, transfers, config)
	log.Info().Dur("duration", time.Since(phaseStart)).Msg("Stop transfer phase completed: platform aliases")

	phaseStart = time.Now()
	buildNearbyWalkTransfers(stops, transfers, config, filterNearbyByService)
	log.Info().Dur("duration", time.Since(phaseStart)).Msg("Stop transfer phase completed: nearby walk scan")

	phaseStart = time.Now()
	if err := writeStopTransfers(ctx, transfers, config); err != nil {
		return err
	}
	log.Info().Dur("duration", time.Since(phaseStart)).Msg("Stop transfer phase completed: write transfers")

	log.Info().
		Int("stops", len(stops)).
		Int("transfers", len(transfers)).
		Int("max_distance_metres", config.MaxDistanceMetres).
		Int("min_change_seconds", config.MinChangeSeconds).
		Int("max_nearby_transfers_per_stop", config.MaxNearbyTransfersPerStop).
		Int("worker_count", config.WorkerCount).
		Float64("walk_speed_metres_per_second", config.WalkSpeedMetresPerSec).
		Dur("duration", time.Since(start)).
		Msg("Built Stop Transfers")

	// lastmodified is also the source-version watermark used by ChangedDatasets.
	// Anchor it before the source scan so an import that completes during this
	// rebuild remains newer and is picked up by the next run. Subtract one
	// millisecond to account for BSON date precision.
	checkpointFields := bson.M{"lastmodified": buildStartedAt.Add(-time.Millisecond)}
	if serviceSnapshotCurrent {
		checkpointFields["stopservicesrevision"] = serviceRevision
		checkpointFields["servicestoprefshash"] = serviceStopRefsHash
	} else {
		// This graph was built without a stable service-stop filter. Force one
		// filtered pass when the materialized view becomes current, even if its
		// membership hash matches the last stable snapshot.
		checkpointFields["servicestoprefshash"] = ""
	}
	if forceRebuild {
		filter["rebuildafter"] = version.RebuildAfter
		result, err := versions.UpdateOne(ctx, filter, bson.M{
			"$set":   checkpointFields,
			"$unset": bson.M{"rebuildafter": ""},
		})
		if err != nil {
			return fmt.Errorf("save stop-transfer rebuild checkpoint: %w", err)
		}
		if result.MatchedCount != 1 {
			return fmt.Errorf("stop-transfer rebuild request changed during build")
		}
		return nil
	}
	_, err = versions.UpdateOne(ctx, filter, bson.M{"$set": checkpointFields}, options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("save stop-transfer build checkpoint: %w", err)
	}
	return nil
}

func checkpointStopServiceMembership(
	ctx context.Context,
	versions *mongo.Collection,
	revision int64,
	hash string,
) error {
	result, err := versions.UpdateOne(ctx,
		bson.M{"dataset": datasetversion.StopTransfersDataset},
		bson.M{"$set": bson.M{
			"stopservicesrevision": revision,
			"servicestoprefshash":  hash,
		}},
	)
	if err != nil {
		return fmt.Errorf("save stop-service membership checkpoint: %w", err)
	}
	if result.MatchedCount != 1 {
		return fmt.Errorf("stop-transfer version disappeared while saving stop-service membership")
	}
	return nil
}

func requestStopTransferRebuild(ctx context.Context) error {
	_, err := database.GetCollection("dataset_versions").UpdateOne(ctx,
		bson.M{"dataset": datasetversion.StopTransfersDataset},
		bson.M{"$max": bson.M{"rebuildafter": time.Now()}},
		options.Update().SetUpsert(true))
	if err != nil {
		return fmt.Errorf("request stop-transfer rebuild: %w", err)
	}
	return nil
}

func stopProvidingDatasetIDs() []string {
	var datasetIDs []string
	for _, dataset := range manager.GetRegisteredDataSets() {
		if dataset.SupportedObjects.Stops &&
			dataset.ImportDestination != datasets.ImportDestinationRealtimeQueue &&
			dataset.ImportDestination != datasets.ImportDestinationSpecificRunner {
			datasetIDs = append(datasetIDs, dataset.Identifier)
		}
	}
	return datasetIDs
}

func ensureStopLinksCurrent(ctx context.Context, changedDatasets []string) error {
	if len(changedDatasets) == 0 {
		return nil
	}
	versions := database.GetCollection("dataset_versions")
	cursor, err := versions.Find(ctx,
		bson.M{"dataset": bson.M{"$in": changedDatasets}},
		options.Find().SetProjection(bson.D{
			{Key: "_id", Value: 0},
			{Key: "lastmodified", Value: 1},
		}),
	)
	if err != nil {
		return fmt.Errorf("load changed stop source versions: %w", err)
	}
	var latestSourceVersion time.Time
	for cursor.Next(ctx) {
		var source struct {
			LastModified time.Time `bson:"lastmodified"`
		}
		if err := cursor.Decode(&source); err != nil {
			cursor.Close(ctx)
			return fmt.Errorf("decode changed stop source version: %w", err)
		}
		if source.LastModified.After(latestSourceVersion) {
			latestSourceVersion = source.LastModified
		}
	}
	if err := cursor.Err(); err != nil {
		cursor.Close(ctx)
		return fmt.Errorf("iterate changed stop source versions: %w", err)
	}
	if err := cursor.Close(ctx); err != nil {
		return fmt.Errorf("close changed stop source versions cursor: %w", err)
	}
	if latestSourceVersion.IsZero() {
		return nil
	}

	var linked ctdf.DatasetVersion
	err = versions.FindOne(ctx, bson.M{"dataset": datasetversion.LinkerDataset("stop")}).Decode(&linked)
	if errors.Is(err, mongo.ErrNoDocuments) || err == nil && !linked.LastModified.After(latestSourceVersion) {
		return fmt.Errorf("link stops after the changed source datasets before rebuilding stop transfers")
	}
	if err != nil {
		return fmt.Errorf("load linked stops checkpoint: %w", err)
	}
	return nil
}

func loadTransferStops(ctx context.Context, serviceStopRefs map[string]struct{}) ([]*transferStop, error) {
	stopsCollection := database.GetCollection("stops")
	query := bson.M{
		"$and": bson.A{
			bson.M{"location.type": "Point"},
			bson.M{"location.coordinates.1": bson.M{"$exists": true}},
			bson.M{
				"$or": bson.A{
					bson.M{"active": true},
					bson.M{"active": bson.M{"$exists": false}},
				},
			},
		},
	}
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
		bson.E{Key: "primaryidentifier", Value: 1},
		bson.E{Key: "otheridentifiers", Value: 1},
		bson.E{Key: "transporttypes", Value: 1},
		bson.E{Key: "location", Value: 1},
		bson.E{Key: "associations", Value: 1},
		bson.E{Key: "platforms", Value: 1},
	})

	cursor, err := stopsCollection.Find(ctx, query, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	stops := make([]*transferStop, 0, 100000)
	for cursor.Next(ctx) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return nil, err
		}
		if stop.PrimaryIdentifier == "" || !validTransferLocation(stop.Location) {
			continue
		}
		hasService := stopHasService(&stop, serviceStopRefs)

		latitude := stop.Location.Coordinates[1]
		longitude := stop.Location.Coordinates[0]
		stops = append(stops, &transferStop{
			PrimaryIdentifier: stop.PrimaryIdentifier,
			Index:             len(stops),
			HasService:        hasService,
			Location:          stop.Location,
			Latitude:          latitude,
			Longitude:         longitude,
			CosLatitude:       math.Cos(latitude * math.Pi / 180),
			Associations:      stop.Associations,
			Platforms:         stop.Platforms,
		})
	}

	if err := cursor.Err(); err != nil {
		return nil, err
	}

	log.Info().Int("stops", len(stops)).Msg("Loaded stops for transfer generation")

	return stops, nil
}

func stopHasService(stop *ctdf.Stop, serviceStopRefs map[string]struct{}) bool {
	if len(serviceStopRefs) == 0 {
		return true
	}
	if _, found := serviceStopRefs[stop.PrimaryIdentifier]; found {
		return true
	}
	for _, stopRef := range stop.OtherIdentifiers {
		if _, found := serviceStopRefs[stopRef]; found {
			return true
		}
	}
	for _, platform := range stop.Platforms {
		if platform == nil {
			continue
		}
		if _, found := serviceStopRefs[platform.PrimaryIdentifier]; found {
			return true
		}
	}
	for _, transportType := range stop.TransportTypes {
		if transportType == ctdf.TransportTypeRail {
			return true
		}
	}
	return false
}

func buildSameStopGroupTransfers(stops []*transferStop, transfers map[transferKey]transferCandidate, config StopTransferBuildConfig) {
	stopGroups := make(map[string][]*transferStop)
	for _, stop := range stops {
		for _, association := range stop.Associations {
			if association == nil || association.AssociatedIdentifier == "" {
				continue
			}
			if association.Type != "" && association.Type != "stop_group" {
				continue
			}

			stopGroups[association.AssociatedIdentifier] = append(stopGroups[association.AssociatedIdentifier], stop)
		}
	}

	maxDistanceMetres := config.MaxDistanceMetres * config.SameStopGroupMultiplier
	if maxDistanceMetres < 1000 {
		maxDistanceMetres = 1000
	}
	maxDistance := float64(maxDistanceMetres)
	// roundedStopDistanceMetres rounds before applying the integer cutoff. Keep
	// the half-metre rounding boundary while avoiding sqrt for rejected pairs.
	maxRoundedDistance := maxDistance + 0.5
	maxDistanceSquared := maxRoundedDistance * maxRoundedDistance

	for _, groupStops := range stopGroups {
		for i := 0; i < len(groupStops); i++ {
			for j := i + 1; j < len(groupStops); j++ {
				fromStop := groupStops[i]
				toStop := groupStops[j]

				distanceSquared := squaredStopDistanceMetres(fromStop, toStop)
				if distanceSquared >= maxDistanceSquared {
					continue
				}
				distance := int(math.Round(math.Sqrt(distanceSquared)))

				addTransfer(transfers, fromStop.PrimaryIdentifier, toStop.PrimaryIdentifier, ctdf.StopTransferTypeSameStopGroup, distance, maxDistanceMetres, config)
				addTransfer(transfers, toStop.PrimaryIdentifier, fromStop.PrimaryIdentifier, ctdf.StopTransferTypeSameStopGroup, distance, maxDistanceMetres, config)
			}
		}
	}

	log.Info().
		Int("stop_groups", len(stopGroups)).
		Int("transfers", len(transfers)).
		Msg("Built same stop-group transfer candidates")
}

func buildPlatformAliasTransfers(stops []*transferStop, transfers map[transferKey]transferCandidate, config StopTransferBuildConfig) {
	stopsByPrimaryIdentifier := make(map[string]*transferStop, len(stops))
	for _, stop := range stops {
		stopsByPrimaryIdentifier[stop.PrimaryIdentifier] = stop
	}

	for _, parentStop := range stops {
		for _, platform := range parentStop.Platforms {
			if platform == nil || platform.PrimaryIdentifier == "" {
				continue
			}

			platformStop := stopsByPrimaryIdentifier[platform.PrimaryIdentifier]
			if platformStop == nil || !validTransferLocation(platformStop.Location) {
				continue
			}
			distance := roundedStopDistanceMetres(parentStop, platformStop)
			addTransfer(transfers, parentStop.PrimaryIdentifier, platformStop.PrimaryIdentifier, ctdf.StopTransferTypePlatformAlias, distance, config.MaxDistanceMetres, config)
			addTransfer(transfers, platformStop.PrimaryIdentifier, parentStop.PrimaryIdentifier, ctdf.StopTransferTypePlatformAlias, distance, config.MaxDistanceMetres, config)
		}
	}

	log.Info().Int("transfers", len(transfers)).Msg("Built platform alias transfer candidates")
}

func buildNearbyWalkTransfers(stops []*transferStop, transfers map[transferKey]transferCandidate, config StopTransferBuildConfig, serviceStopsOnly bool) {
	nearbyStops := make([]*transferStop, 0, len(stops))
	for _, stop := range stops {
		if !serviceStopsOnly || stop.HasService {
			nearbyStops = append(nearbyStops, stop)
		}
	}
	if len(nearbyStops) == 0 {
		return
	}

	cellDegrees := float64(config.MaxDistanceMetres) / metresPerDegree
	if cellDegrees <= 0 {
		return
	}

	grid := make(map[gridKey][]*transferStop, len(nearbyStops))
	for _, stop := range nearbyStops {
		key := stopGridKey(stop, cellDegrees)
		grid[key] = append(grid[key], stop)
	}

	maxDistance := float64(config.MaxDistanceMetres)
	maxDistanceSquared := maxDistance * maxDistance

	// Discover candidate transfers in parallel. Each worker owns a contiguous
	// chunk of origin stops and keeps only the nearest candidates for those stops
	// while scanning, so dense areas do not accumulate all possible nearby pairs
	// before applying MaxNearbyTransfersPerStop. transfers is read-only during discovery
	// (only written in buildSameStopGroup/buildPlatformAlias beforehand and in the
	// emit phase afterwards), so concurrent transferExists reads are safe.
	workerCount := config.WorkerCount
	if workerCount < 1 {
		workerCount = 1
	}
	if workerCount > len(nearbyStops) {
		workerCount = len(nearbyStops)
	}
	chunkSize := (len(nearbyStops) + workerCount - 1) / workerCount
	latRange := int(math.Ceil((float64(config.MaxDistanceMetres) / metresPerDegree) / cellDegrees))
	if latRange < 1 {
		latRange = 1
	}

	discoveryPool := pool.NewWithResults[nearbyDiscoveryResult]().WithMaxGoroutines(workerCount)

	for start := 0; start < len(nearbyStops); start += chunkSize {
		end := start + chunkSize
		if end > len(nearbyStops) {
			end = len(nearbyStops)
		}
		chunk := nearbyStops[start:end]

		discoveryPool.Go(func() nearbyDiscoveryResult {
			result := nearbyDiscoveryResult{
				stopCandidates: make([]nearbyStopCandidates, 0, len(chunk)),
			}

			for _, fromStop := range chunk {
				stopCandidates := make([]nearbyTransferCandidate, 0, minInt(config.MaxNearbyTransfersPerStop, 8))
				fromKey := stopGridKey(fromStop, cellDegrees)

				cosLat := fromStop.CosLatitude
				if math.Abs(cosLat) < 0.01 {
					cosLat = 0.01
				}
				lonRange := int(math.Ceil((float64(config.MaxDistanceMetres) / (metresPerDegree * math.Abs(cosLat))) / cellDegrees))
				if lonRange < 1 {
					lonRange = 1
				}

				for latOffset := -latRange; latOffset <= latRange; latOffset++ {
					for lonOffset := -lonRange; lonOffset <= lonRange; lonOffset++ {
						candidates := grid[gridKey{
							lat: fromKey.lat + latOffset,
							lon: fromKey.lon + lonOffset,
						}]

						for _, toStop := range candidates {
							if fromStop.PrimaryIdentifier == toStop.PrimaryIdentifier {
								continue
							}
							if transferExists(transfers, fromStop.PrimaryIdentifier, toStop.PrimaryIdentifier) {
								continue
							}

							distanceSquared := squaredStopDistanceMetres(fromStop, toStop)
							if distanceSquared > maxDistanceSquared {
								continue
							}

							distance := int(math.Round(math.Sqrt(distanceSquared)))
							addNearbyCandidate(&stopCandidates, toStop.Index, stops, distance, config.MaxNearbyTransfersPerStop)
							result.candidateEdges++
						}
					}
				}

				result.stopCandidates = append(result.stopCandidates, nearbyStopCandidates{
					stopIndex:  fromStop.Index,
					candidates: stopCandidates,
				})
			}

			return result
		})
	}

	discoveredResults := discoveryPool.Wait()
	nearbyCandidateEdges := 0
	addedNearbyTransfers := 0
	for _, result := range discoveredResults {
		nearbyCandidateEdges += result.candidateEdges
		for _, stopCandidates := range result.stopCandidates {
			fromStop := stops[stopCandidates.stopIndex]
			// addNearbyCandidate already retains the nearest bounded set, so there
			// is no need to sort the candidates before adding them.
			for _, candidate := range stopCandidates.candidates {
				addTransfer(transfers, fromStop.PrimaryIdentifier, stops[candidate.toStopIndex].PrimaryIdentifier, ctdf.StopTransferTypeNearbyWalk, candidate.distanceMetres, config.MaxDistanceMetres, config)
				addedNearbyTransfers++
			}
		}
	}

	log.Info().
		Int("nearby_candidate_edges", nearbyCandidateEdges).
		Int("nearby_transfers_added", addedNearbyTransfers).
		Int("transfers", len(transfers)).
		Msg("Built nearby walk transfer candidates")
}

func addNearbyCandidate(
	candidates *[]nearbyTransferCandidate,
	toStopIndex int,
	stops []*transferStop,
	distanceMetres int,
	maxCandidates int,
) {
	if maxCandidates <= 0 || candidates == nil || toStopIndex < 0 || toStopIndex >= len(stops) {
		return
	}

	candidate := nearbyTransferCandidate{
		toStopIndex:    toStopIndex,
		distanceMetres: distanceMetres,
	}
	if len(*candidates) < maxCandidates {
		*candidates = append(*candidates, candidate)
		return
	}

	worstIndex := 0
	for i := 1; i < len(*candidates); i++ {
		if nearbyCandidateLess((*candidates)[worstIndex], (*candidates)[i], stops) {
			worstIndex = i
		}
	}

	if nearbyCandidateLess(candidate, (*candidates)[worstIndex], stops) {
		(*candidates)[worstIndex] = candidate
	}
}

func nearbyCandidateLess(a nearbyTransferCandidate, b nearbyTransferCandidate, stops []*transferStop) bool {
	if a.distanceMetres == b.distanceMetres {
		return stops[a.toStopIndex].PrimaryIdentifier < stops[b.toStopIndex].PrimaryIdentifier
	}
	return a.distanceMetres < b.distanceMetres
}

func writeStopTransfers(ctx context.Context, transfers map[transferKey]transferCandidate, config StopTransferBuildConfig) error {
	stopTransfersCollection := database.GetCollection("stop_transfers")

	operations := make([]mongo.WriteModel, 0, config.BatchSize)
	// Truncated to the millisecond so it round-trips through BSON exactly: the
	// stale-delete below relies on this run's documents comparing not-less-than
	// `now`, which only holds if the stored value has no sub-millisecond part.
	now := time.Now().Truncate(time.Millisecond)
	datasource := &ctdf.DataSourceReference{
		OriginalFormat: stopTransferGeneratorFormat,
		ProviderName:   "Travigo",
		ProviderID:     "travigo",
		DatasetID:      "travigo-stop-transfers",
		Timestamp:      now.Format(time.RFC3339),
	}

	// The unordered upserts are independent, so map iteration avoids allocating
	// and sorting a second slice containing every generated edge.
	for key, candidate := range transfers {
		identifier := fmt.Sprintf("generated-"+ctdf.StopTransferIDFormat, key.from, key.to)
		transfer := ctdf.StopTransfer{
			PrimaryIdentifier:        identifier,
			FromStopRef:              key.from,
			ToStopRef:                key.to,
			Type:                     candidate.transferType,
			DistanceMetres:           candidate.distanceMetres,
			WalkDurationSeconds:      candidate.walkDurationSeconds,
			MinChangeDurationSeconds: candidate.minChangeDurationSeconds,
			TotalDurationSeconds:     candidate.totalDurationSeconds,
			GeneratedRadiusMetres:    candidate.generatedRadiusMetres,
			CreationDateTime:         now,
			ModificationDateTime:     now,
			DataSource:               datasource,
		}

		updateModel := mongo.NewReplaceOneModel().
			SetFilter(bson.M{"primaryidentifier": identifier}).
			SetReplacement(transfer).
			SetUpsert(true)

		operations = append(operations, updateModel)
		if len(operations) >= config.BatchSize {
			if err := flushStopTransferBatch(ctx, stopTransfersCollection, operations); err != nil {
				return err
			}
			operations = operations[:0]
		}
	}

	if len(operations) > 0 {
		if err := flushStopTransferBatch(ctx, stopTransfersCollection, operations); err != nil {
			return err
		}
	}

	// Remove transfers from previous runs that were not re-written by this full
	// graph refresh, including transfers whose stop was removed from the input.
	// Upserting first and deleting stale afterwards keeps the collection fully
	// populated throughout the rebuild, so concurrent readers (the journey
	// planner) never see an empty transfers set.
	staleQuery := bson.M{
		"datasource.originalformat": stopTransferGeneratorFormat,
		"modificationdatetime":      bson.M{"$lt": now},
	}
	deleted, err := stopTransfersCollection.DeleteMany(ctx, staleQuery)
	if err != nil {
		return err
	}
	log.Info().Int64("deleted", deleted.DeletedCount).Msg("Deleted stale Stop Transfers")

	return nil
}

func flushStopTransferBatch(ctx context.Context, collection *mongo.Collection, operations []mongo.WriteModel) error {
	ordered := false
	_, err := collection.BulkWrite(ctx, operations, &options.BulkWriteOptions{Ordered: &ordered})
	if err != nil {
		return err
	}

	log.Info().Int("transfers", len(operations)).Msg("Wrote Stop Transfer batch")
	return nil
}

func addTransfer(
	transfers map[transferKey]transferCandidate,
	fromStopRef string,
	toStopRef string,
	transferType ctdf.StopTransferType,
	distanceMetres int,
	generatedRadiusMetres int,
	config StopTransferBuildConfig,
) {
	if fromStopRef == "" || toStopRef == "" || fromStopRef == toStopRef {
		return
	}

	walkDurationSeconds := int(math.Ceil(float64(distanceMetres) / config.WalkSpeedMetresPerSec))
	if walkDurationSeconds < 0 {
		walkDurationSeconds = 0
	}

	candidate := transferCandidate{
		key: transferKey{
			from: fromStopRef,
			to:   toStopRef,
		},
		transferType:             transferType,
		distanceMetres:           distanceMetres,
		walkDurationSeconds:      walkDurationSeconds,
		minChangeDurationSeconds: config.MinChangeSeconds,
		totalDurationSeconds:     walkDurationSeconds + config.MinChangeSeconds,
		generatedRadiusMetres:    generatedRadiusMetres,
		priority:                 transferTypePriority(transferType),
	}

	existing, exists := transfers[candidate.key]
	if !exists ||
		candidate.totalDurationSeconds < existing.totalDurationSeconds ||
		(candidate.totalDurationSeconds == existing.totalDurationSeconds && candidate.priority < existing.priority) {
		transfers[candidate.key] = candidate
	}
}

func transferTypePriority(transferType ctdf.StopTransferType) int {
	switch transferType {
	case ctdf.StopTransferTypePlatformAlias:
		return 0
	case ctdf.StopTransferTypeSameStopGroup:
		return 1
	default:
		return 2
	}
}

func stopGridKey(stop *transferStop, cellDegrees float64) gridKey {
	return gridKey{
		lat: int(math.Floor(stop.Latitude / cellDegrees)),
		lon: int(math.Floor(stop.Longitude / cellDegrees)),
	}
}

func transferExists(transfers map[transferKey]transferCandidate, fromStopRef string, toStopRef string) bool {
	_, exists := transfers[transferKey{from: fromStopRef, to: toStopRef}]
	return exists
}

func defaultStopTransferWorkerCount() int {
	return stopTransferWorkerCount(runtime.NumCPU())
}

func stopTransferWorkerCount(cpuCount int) int {
	if cpuCount <= 1 {
		return 1
	}

	return cpuCount / 2
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}

	return b
}

func validTransferLocation(location *ctdf.Location) bool {
	if location == nil || len(location.Coordinates) < 2 {
		return false
	}
	if location.Type != "" && location.Type != "Point" {
		return false
	}

	lon := location.Coordinates[0]
	lat := location.Coordinates[1]
	if math.IsNaN(lon) || math.IsNaN(lat) || math.IsInf(lon, 0) || math.IsInf(lat, 0) {
		return false
	}

	return lon >= -180 && lon <= 180 && lat >= -90 && lat <= 90
}

func roundedStopDistanceMetres(from *transferStop, to *transferStop) int {
	return int(math.Round(math.Sqrt(squaredStopDistanceMetres(from, to))))
}

func squaredStopDistanceMetres(from *transferStop, to *transferStop) float64 {
	latMetres := (to.Latitude - from.Latitude) * metresPerDegree
	lonMetres := (to.Longitude - from.Longitude) * metresPerDegree * from.CosLatitude

	return (latMetres * latMetres) + (lonMetres * lonMetres)
}
