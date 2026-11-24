package datalinker

import (
	"context"
	"fmt"
	"math"
	"slices"

	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	transferSnapshotTileDegrees = 0.05
	maxTransferLocalityTiles    = 12000
	maxTransferLocalityIDs      = 10000
)

func transferSnapshotTile(lat, lon float64) string {
	// Normalize longitude so equivalent points around the date line share the
	// same bounded tile coordinate space.
	lon = math.Mod(lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	lon -= 180
	latIndex := int(math.Floor((lat + 90) / transferSnapshotTileDegrees))
	lonIndex := int(math.Floor((lon + 180) / transferSnapshotTileDegrees))
	return fmt.Sprintf("%d:%d", latIndex, lonIndex)
}

// loadTransferNeighbourhood adds snapshots that could affect transfers around
// the supplied stops. Existing entries win because callers may have newer
// source snapshots than those persisted by the previous build.
func loadTransferNeighbourhood(ctx context.Context, seeds []transferSnapshot, radius int, into map[string]transferSnapshot) error {
	if len(seeds) == 0 {
		return nil
	}
	tiles, ids := transferLocalityCandidates(seeds, radius)
	coll := database.GetCollection(transferSnapshotCollection)
	filter := transferLocalityFilter(tiles, ids)
	return loadTransferSnapshotQuery(ctx, coll, filter, into)
}

func loadTransferSnapshotIDs(ctx context.Context, ids []string, into map[string]transferSnapshot) error {
	ids = uniqueNonempty(ids)
	for batch := range slices.Chunk(ids, transferReadBatch) {
		if err := loadTransferSnapshotQuery(ctx, database.GetCollection(transferSnapshotCollection), bson.M{"_id": bson.M{"$in": batch}}, into); err != nil {
			return err
		}
	}
	return nil
}

func loadTransferSnapshotQuery(ctx context.Context, coll *mongo.Collection, filter bson.M, into map[string]transferSnapshot) error {
	if into == nil {
		return fmt.Errorf("transfer snapshot destination map is nil")
	}
	cur, err := coll.Find(ctx, filter)
	if err != nil {
		return fmt.Errorf("read transfer neighbourhood snapshots: %w", err)
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var snapshot transferSnapshot
		if err := cur.Decode(&snapshot); err != nil {
			return fmt.Errorf("decode transfer neighbourhood snapshot: %w", err)
		}
		if _, exists := into[snapshot.ID]; !exists {
			into[snapshot.ID] = snapshot
		}
	}
	if err := cur.Err(); err != nil {
		return fmt.Errorf("read transfer neighbourhood snapshots: %w", err)
	}
	return nil
}

func transferLocalityFilter(tiles, ids []string) bson.M {
	if tiles == nil {
		return bson.M{}
	}
	clauses := bson.A{}
	if len(tiles) > 0 {
		clauses = append(clauses, bson.M{"tile": bson.M{"$in": tiles}})
	}
	if len(ids) > 0 {
		values := ids
		clauses = append(clauses,
			bson.M{"_id": bson.M{"$in": values}},
			bson.M{"associations": bson.M{"$in": values}},
			bson.M{"platforms": bson.M{"$in": values}},
		)
	}
	if len(clauses) == 0 {
		return bson.M{}
	}
	return bson.M{"$or": clauses}
}

// A nil tile slice signals that the tile expansion exceeded its bound and the
// caller should use a full scan. IDs include both directions of references.
func transferLocalityCandidates(seeds []transferSnapshot, radius int) ([]string, []string) {
	if radius < 0 {
		radius = 0
	}
	latDelta := float64(radius) / 110500.0
	tileSet := make(map[string]struct{})
	idSet := make(map[string]struct{})
	for _, seed := range seeds {
		for _, id := range seed.Associations {
			if id != "" {
				idSet[id] = struct{}{}
			}
		}
		for _, id := range seed.Platforms {
			if id != "" {
				idSet[id] = struct{}{}
			}
		}
		if seed.ID != "" {
			idSet[seed.ID] = struct{}{}
		}

		minLat := math.Max(-90, seed.Latitude-latDelta)
		maxLat := math.Min(90, seed.Latitude+latDelta)
		lat0 := int(math.Floor((minLat + 90) / transferSnapshotTileDegrees))
		lat1 := int(math.Floor((maxLat + 90) / transferSnapshotTileDegrees))
		maxAbsLat := math.Max(math.Abs(minLat), math.Abs(maxLat))
		lonRange := 180.0
		if maxAbsLat < 89.999999 {
			lonRange = math.Min(180, float64(radius)/(110500*math.Cos(maxAbsLat*math.Pi/180)))
		}
		lonRange += transferSnapshotTileDegrees // include edge tiles touched by a point radius
		center := normalizedLongitude(seed.Longitude)
		lon0 := int(math.Floor((center - lonRange + 180) / transferSnapshotTileDegrees))
		lon1 := int(math.Floor((center + lonRange + 180) / transferSnapshotTileDegrees))
		if lonRange >= 180 {
			lon0, lon1 = 0, int(math.Round(360/transferSnapshotTileDegrees))-1
		}
		for lat := lat0; lat <= lat1; lat++ {
			for lon := lon0; lon <= lon1; lon++ {
				wrapped := lon % int(math.Round(360/transferSnapshotTileDegrees))
				if wrapped < 0 {
					wrapped += int(math.Round(360 / transferSnapshotTileDegrees))
				}
				tileSet[fmt.Sprintf("%d:%d", lat, wrapped)] = struct{}{}
				if len(tileSet) > maxTransferLocalityTiles || len(idSet) > maxTransferLocalityIDs {
					return nil, nil
				}
			}
		}
	}
	tiles, ids := make([]string, 0, len(tileSet)), make([]string, 0, len(idSet))
	for tile := range tileSet {
		tiles = append(tiles, tile)
	}
	for id := range idSet {
		ids = append(ids, id)
	}
	return tiles, ids
}

func normalizedLongitude(lon float64) float64 {
	lon = math.Mod(lon+180, 360)
	if lon < 0 {
		lon += 360
	}
	return lon - 180
}

func uniqueNonempty(ids []string) []string {
	seen := make(map[string]struct{}, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

func ensureTransferLocalityIndexes(ctx context.Context) error {
	coll := database.GetCollection(transferSnapshotCollection)
	indexes := []mongo.IndexModel{
		{Keys: bson.D{{Key: "tile", Value: 1}}},
		{Keys: bson.D{{Key: "associations", Value: 1}}},
		{Keys: bson.D{{Key: "platforms", Value: 1}}},
	}
	if _, err := coll.Indexes().CreateMany(ctx, indexes, options.CreateIndexes()); err != nil {
		return fmt.Errorf("ensure transfer snapshot locality indexes: %w", err)
	}
	return nil
}
