package datalinker

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
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
	metresPerDegree                             = 111320.0
)

type StopTransferBuildConfig struct {
	MaxDistanceMetres         int
	MinChangeSeconds          int
	WalkSpeedMetresPerSec     float64
	BatchSize                 int
	MaxNearbyTransfersPerStop int
	SameStopGroupMultiplier   int
}

type transferStop struct {
	PrimaryIdentifier string
	Index             int
	Location          *ctdf.Location
	Latitude          float64
	Longitude         float64
	CosLatitude       float64
	Associations      []*ctdf.Association
	Platforms         []*ctdf.StopPlatform
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

type nearbyPair struct {
	fromIndex int
	toIndex   int
	distance  int
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
	if config.SameStopGroupMultiplier <= 0 {
		config.SameStopGroupMultiplier = 2
	}

	return config
}

func BuildStopTransfers(config StopTransferBuildConfig) error {
	config = config.withDefaults()
	ctx := context.Background()
	start := time.Now()

	stops, err := loadTransferStops(ctx)
	if err != nil {
		return err
	}

	transfers := make(map[transferKey]transferCandidate)

	buildSameStopGroupTransfers(stops, transfers, config)
	buildPlatformAliasTransfers(stops, transfers, config)
	buildNearbyWalkTransfers(stops, transfers, config)

	if err := writeStopTransfers(ctx, transfers, config); err != nil {
		return err
	}

	log.Info().
		Int("stops", len(stops)).
		Int("transfers", len(transfers)).
		Int("max_distance_metres", config.MaxDistanceMetres).
		Int("min_change_seconds", config.MinChangeSeconds).
		Int("max_nearby_transfers_per_stop", config.MaxNearbyTransfersPerStop).
		Float64("walk_speed_metres_per_second", config.WalkSpeedMetresPerSec).
		Dur("duration", time.Since(start)).
		Msg("Built Stop Transfers")

	return nil
}

func loadTransferStops(ctx context.Context) ([]*transferStop, error) {
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

		latitude := stop.Location.Coordinates[1]
		longitude := stop.Location.Coordinates[0]
		stops = append(stops, &transferStop{
			PrimaryIdentifier: stop.PrimaryIdentifier,
			Index:             len(stops),
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

	for _, groupStops := range stopGroups {
		for i := 0; i < len(groupStops); i++ {
			for j := i + 1; j < len(groupStops); j++ {
				fromStop := groupStops[i]
				toStop := groupStops[j]

				distance := roundedStopDistanceMetres(fromStop, toStop)
				if distance > maxDistanceMetres {
					continue
				}

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

func buildNearbyWalkTransfers(stops []*transferStop, transfers map[transferKey]transferCandidate, config StopTransferBuildConfig) {
	if len(stops) == 0 {
		return
	}

	cellDegrees := float64(config.MaxDistanceMetres) / metresPerDegree
	if cellDegrees <= 0 {
		return
	}

	grid := make(map[gridKey][]*transferStop, len(stops))
	for _, stop := range stops {
		key := stopGridKey(stop, cellDegrees)
		grid[key] = append(grid[key], stop)
	}

	maxDistance := float64(config.MaxDistanceMetres)
	maxDistanceSquared := maxDistance * maxDistance

	// Discover candidate pairs in parallel. Each worker owns a contiguous chunk
	// of stops (by index) and only emits pairs where fromStop.Index < toStop.Index,
	// so no pair is discovered twice. transfers is read-only during discovery
	// (only written in buildSameStopGroup/buildPlatformAlias beforehand and in the
	// emit phase afterwards), so concurrent transferExists reads are safe. Results
	// are returned in chunk order, so the subsequent merge is identical to a
	// single-threaded scan.
	workerCount := runtime.NumCPU()
	if workerCount < 1 {
		workerCount = 1
	}
	chunkSize := (len(stops) + workerCount - 1) / workerCount

	discoveryPool := pool.NewWithResults[[]nearbyPair]().WithMaxGoroutines(workerCount)

	for start := 0; start < len(stops); start += chunkSize {
		end := start + chunkSize
		if end > len(stops) {
			end = len(stops)
		}
		chunk := stops[start:end]

		discoveryPool.Go(func() []nearbyPair {
			pairs := make([]nearbyPair, 0, len(chunk)*config.MaxNearbyTransfersPerStop)

			for _, fromStop := range chunk {
				fromKey := stopGridKey(fromStop, cellDegrees)
				latRange := int(math.Ceil((float64(config.MaxDistanceMetres) / metresPerDegree) / cellDegrees))
				if latRange < 1 {
					latRange = 1
				}

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
							if fromStop.Index >= toStop.Index {
								continue
							}
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
							pairs = append(pairs, nearbyPair{
								fromIndex: fromStop.Index,
								toIndex:   toStop.Index,
								distance:  distance,
							})
						}
					}
				}
			}

			return pairs
		})
	}

	discoveredPairs := discoveryPool.Wait()

	nearbyCandidatesByStop := make([][]nearbyTransferCandidate, len(stops))
	nearbyPairCandidates := 0
	for _, pairs := range discoveredPairs {
		for _, pair := range pairs {
			addNearbyCandidate(nearbyCandidatesByStop, pair.fromIndex, pair.toIndex, stops, pair.distance, config.MaxNearbyTransfersPerStop)
			addNearbyCandidate(nearbyCandidatesByStop, pair.toIndex, pair.fromIndex, stops, pair.distance, config.MaxNearbyTransfersPerStop)
			nearbyPairCandidates++
		}
	}

	addedNearbyTransfers := 0
	for _, fromStop := range stops {
		candidates := nearbyCandidatesByStop[fromStop.Index]
		sort.Slice(candidates, func(i, j int) bool {
			return nearbyCandidateLess(candidates[i], candidates[j], stops)
		})

		if len(candidates) > config.MaxNearbyTransfersPerStop {
			candidates = candidates[:config.MaxNearbyTransfersPerStop]
		}

		for _, candidate := range candidates {
			addTransfer(transfers, fromStop.PrimaryIdentifier, stops[candidate.toStopIndex].PrimaryIdentifier, ctdf.StopTransferTypeNearbyWalk, candidate.distanceMetres, config.MaxDistanceMetres, config)
			addedNearbyTransfers++
		}
	}

	log.Info().
		Int("nearby_pair_candidates", nearbyPairCandidates).
		Int("nearby_transfers_added", addedNearbyTransfers).
		Int("transfers", len(transfers)).
		Msg("Built nearby walk transfer candidates")
}

func addNearbyCandidate(
	candidatesByStop [][]nearbyTransferCandidate,
	fromStopIndex int,
	toStopIndex int,
	stops []*transferStop,
	distanceMetres int,
	maxCandidates int,
) {
	if maxCandidates <= 0 || fromStopIndex < 0 || fromStopIndex >= len(candidatesByStop) || toStopIndex < 0 || toStopIndex >= len(stops) {
		return
	}

	candidate := nearbyTransferCandidate{
		toStopIndex:    toStopIndex,
		distanceMetres: distanceMetres,
	}
	candidates := candidatesByStop[fromStopIndex]
	if len(candidates) < maxCandidates {
		candidatesByStop[fromStopIndex] = append(candidates, candidate)
		return
	}

	worstIndex := 0
	for i := 1; i < len(candidates); i++ {
		if nearbyCandidateLess(candidates[worstIndex], candidates[i], stops) {
			worstIndex = i
		}
	}

	if nearbyCandidateLess(candidate, candidates[worstIndex], stops) {
		candidates[worstIndex] = candidate
		candidatesByStop[fromStopIndex] = candidates
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

	keys := make([]transferKey, 0, len(transfers))
	for key := range transfers {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].from == keys[j].from {
			return keys[i].to < keys[j].to
		}
		return keys[i].from < keys[j].from
	})

	operations := make([]mongo.WriteModel, 0, config.BatchSize)
	// Truncated to the millisecond so it round-trips through BSON exactly: the
	// stale-delete below relies on this run's documents comparing not-less-than
	// `now`, which only holds if the stored value has no sub-millisecond part.
	now := time.Now().Truncate(time.Millisecond)
	datasource := &ctdf.DataSourceReference{
		OriginalFormat: "travigo-stop-transfer-generator",
		ProviderName:   "Travigo",
		ProviderID:     "travigo",
		DatasetID:      "travigo-stop-transfers",
		Timestamp:      now.Format(time.RFC3339),
	}

	for _, key := range keys {
		candidate := transfers[key]
		identifier := fmt.Sprintf(ctdf.StopTransferIDFormat, candidate.key.from, candidate.key.to)
		transfer := ctdf.StopTransfer{
			PrimaryIdentifier:                 identifier,
			FromStopRef:                       candidate.key.from,
			ToStopRef:                         candidate.key.to,
			Type:                              candidate.transferType,
			DistanceMetres:                    candidate.distanceMetres,
			WalkDurationSeconds:               candidate.walkDurationSeconds,
			MinChangeDurationSeconds:          candidate.minChangeDurationSeconds,
			TotalDurationSeconds:              candidate.totalDurationSeconds,
			GeneratedRadiusMetres:             candidate.generatedRadiusMetres,
			GeneratedWalkSpeedMetresPerSecond: config.WalkSpeedMetresPerSec,
			CreationDateTime:                  now,
			ModificationDateTime:              now,
			DataSource:                        datasource,
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

	// Remove transfers from previous runs that were not re-written this run.
	// Upserting first and deleting stale afterwards keeps the collection fully
	// populated throughout the rebuild, so concurrent readers (the journey
	// planner) never see an empty transfers set.
	deleted, err := stopTransfersCollection.DeleteMany(ctx, bson.M{
		"modificationdatetime": bson.M{"$lt": now},
	})
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
