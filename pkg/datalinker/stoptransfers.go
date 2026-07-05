package datalinker

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
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
	defaultStopTransferBatchSize                = 5000
	metresPerDegree                             = 111320.0
	earthRadiusMetres                           = 6378100.0
)

type StopTransferBuildConfig struct {
	MaxDistanceMetres       int
	MinChangeSeconds        int
	WalkSpeedMetresPerSec   float64
	BatchSize               int
	SameStopGroupMultiplier int
}

type transferStop struct {
	PrimaryIdentifier string
	Location          *ctdf.Location
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

		stops = append(stops, &transferStop{
			PrimaryIdentifier: stop.PrimaryIdentifier,
			Location:          stop.Location,
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
		for i, fromStop := range groupStops {
			for j, toStop := range groupStops {
				if i == j {
					continue
				}

				distance := roundedDistanceMetres(fromStop.Location, toStop.Location)
				if distance > maxDistanceMetres {
					continue
				}

				addTransfer(transfers, fromStop.PrimaryIdentifier, toStop.PrimaryIdentifier, ctdf.StopTransferTypeSameStopGroup, distance, maxDistanceMetres, config)
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

			distance := roundedDistanceMetres(parentStop.Location, platformStop.Location)
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

	for _, fromStop := range stops {
		fromLat := fromStop.Location.Coordinates[1]
		fromKey := stopGridKey(fromStop, cellDegrees)
		latRange := int(math.Ceil((float64(config.MaxDistanceMetres) / metresPerDegree) / cellDegrees))
		if latRange < 1 {
			latRange = 1
		}

		cosLat := math.Cos(fromLat * math.Pi / 180)
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

					distance := roundedDistanceMetres(fromStop.Location, toStop.Location)
					if distance > config.MaxDistanceMetres {
						continue
					}

					addTransfer(transfers, fromStop.PrimaryIdentifier, toStop.PrimaryIdentifier, ctdf.StopTransferTypeNearbyWalk, distance, config.MaxDistanceMetres, config)
				}
			}
		}
	}

	log.Info().Int("transfers", len(transfers)).Msg("Built nearby walk transfer candidates")
}

func writeStopTransfers(ctx context.Context, transfers map[transferKey]transferCandidate, config StopTransferBuildConfig) error {
	stopTransfersCollection := database.GetCollection("stop_transfers")

	deleted, err := stopTransfersCollection.DeleteMany(ctx, bson.M{})
	if err != nil {
		return err
	}
	log.Info().Int64("deleted", deleted.DeletedCount).Msg("Deleted existing Stop Transfers")

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
	now := time.Now()
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
		lat: int(math.Floor(stop.Location.Coordinates[1] / cellDegrees)),
		lon: int(math.Floor(stop.Location.Coordinates[0] / cellDegrees)),
	}
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

func roundedDistanceMetres(from *ctdf.Location, to *ctdf.Location) int {
	return int(math.Round(distanceMetres(from, to)))
}

func distanceMetres(from *ctdf.Location, to *ctdf.Location) float64 {
	fromLat := from.Coordinates[1] * math.Pi / 180
	fromLon := from.Coordinates[0] * math.Pi / 180
	toLat := to.Coordinates[1] * math.Pi / 180
	toLon := to.Coordinates[0] * math.Pi / 180

	dLat := toLat - fromLat
	dLon := toLon - fromLon

	a := math.Pow(math.Sin(dLat/2), 2) +
		math.Cos(fromLat)*math.Cos(toLat)*math.Pow(math.Sin(dLon/2), 2)

	return 2 * earthRadiusMetres * math.Asin(math.Sqrt(a))
}
