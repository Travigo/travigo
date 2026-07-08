package realtimestore

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

func setupMemoryTestRedis(t *testing.T) *miniredis.Miniredis {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousClient := redis_client.Client
	redis_client.Client = client

	t.Cleanup(func() {
		redis_client.Client = previousClient
		_ = client.Close()
	})

	return server
}

func TestSaveRealtimeJourneyDefaultsZeroExpiration(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{PrimaryIdentifier: "zero-timeout"}

	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	ttl := server.TTL(realtimeJourneyDetailsKey(journey.PrimaryIdentifier))
	if ttl != defaultRealtimeJourneyTTL {
		t.Fatalf("expected TTL %s, got %s", defaultRealtimeJourneyTTL, ttl)
	}
}

func TestSaveRealtimeJourneyPreservesPositiveExpiration(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "rail-journey",
		TimeoutDurationMinutes: 181,
	}

	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	expected := 181 * time.Minute
	if ttl := server.TTL(realtimeJourneyDetailsKey(journey.PrimaryIdentifier)); ttl != expected {
		t.Fatalf("expected TTL %s, got %s", expected, ttl)
	}
}

func TestRailAllocationRemainsAvailableForDay(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	detailed := ctdf.JourneyDetailedRail{
		Carriages: []ctdf.RailCarriage{{ID: "A"}},
	}

	if err := UpdateRailDetailedAllocation(ctx, "rail-journey", detailed); err != nil {
		t.Fatalf("update rail allocation: %v", err)
	}

	key := realtimeJourneyRailDetailedKey("allocation", "rail-journey")
	if ttl := server.TTL(key); ttl != railAllocationTTL {
		t.Fatalf("expected TTL %s, got %s", railAllocationTTL, ttl)
	}
}

func TestRailLoadingUsesRealtimeJourneyExpiration(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	journey := &ctdf.RealtimeJourney{
		PrimaryIdentifier:      "rail-journey",
		TimeoutDurationMinutes: 181,
	}
	if err := SaveRealtimeJourney(ctx, journey); err != nil {
		t.Fatalf("save realtime journey: %v", err)
	}

	detailed := ctdf.JourneyDetailedRail{
		Carriages: []ctdf.RailCarriage{{ID: "A", Occupancy: 42}},
	}
	if err := UpdateRailDetailedLoading(ctx, journey.PrimaryIdentifier, detailed); err != nil {
		t.Fatalf("update rail loading: %v", err)
	}

	expected := 181 * time.Minute
	key := realtimeJourneyRailDetailedKey("loading", journey.PrimaryIdentifier)
	if ttl := server.TTL(key); ttl != expected {
		t.Fatalf("expected TTL %s, got %s", expected, ttl)
	}
}

func TestCleanupStaleLocationIndexKeepsLiveLocations(t *testing.T) {
	setupMemoryTestRedis(t)
	ctx := context.Background()
	indexKey := realtimeJourneyLocationGeoIndexKey()

	if err := redis_client.Client.GeoAdd(ctx, indexKey,
		&redis.GeoLocation{Name: "live", Longitude: -0.1, Latitude: 51.5},
		&redis.GeoLocation{Name: "expired", Longitude: -0.2, Latitude: 51.6},
	).Err(); err != nil {
		t.Fatalf("seed geo index: %v", err)
	}
	if err := redis_client.Client.Set(
		ctx,
		realtimeJourneyLocationKey("live"),
		`{"location":{"type":"Point","coordinates":[-0.1,51.5]}}`,
		time.Minute,
	).Err(); err != nil {
		t.Fatalf("seed live location: %v", err)
	}

	removed, err := CleanupStaleLocationIndex(ctx)
	if err != nil {
		t.Fatalf("clean location index: %v", err)
	}
	if removed != 1 {
		t.Fatalf("expected one removal, got %d", removed)
	}

	members, err := redis_client.Client.ZRange(ctx, indexKey, 0, -1).Result()
	if err != nil {
		t.Fatalf("read geo index: %v", err)
	}
	if len(members) != 1 || members[0] != "live" {
		t.Fatalf("expected only live member, got %v", members)
	}
}
