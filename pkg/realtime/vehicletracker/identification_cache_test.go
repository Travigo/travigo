package vehicletracker

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/redis_client"
)

func setupIdentificationTestRedis(t *testing.T) {
	t.Helper()

	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	previousClient := redis_client.Client
	redis_client.Client = client
	t.Cleanup(func() {
		redis_client.Client = previousClient
		_ = client.Close()
	})
}

func TestIdentificationMappingAcceptsOnlyNewerUpdates(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	initial := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	newer := initial.Add(time.Minute)

	storeIdentificationMapping(ctx, "vehicle-1", "journey-1", initial)

	journeyID, found, err := touchExistingIdentificationMapping(ctx, "vehicle-1", newer)
	if err != nil {
		t.Fatalf("touch newer mapping: %v", err)
	}
	if !found || journeyID != "journey-1" {
		t.Fatalf("expected journey mapping, got id=%q found=%t", journeyID, found)
	}

	journeyID, found, err = touchExistingIdentificationMapping(ctx, "vehicle-1", initial)
	if err != nil {
		t.Fatalf("touch stale mapping: %v", err)
	}
	if !found || journeyID != "" {
		t.Fatalf("expected stale update to be ignored, got id=%q found=%t", journeyID, found)
	}
}

func TestIdentificationMappingReturnsMissAndSuppressesFailures(t *testing.T) {
	setupIdentificationTestRedis(t)
	ctx := context.Background()
	now := time.Now()

	journeyID, found, err := touchExistingIdentificationMapping(ctx, "missing", now)
	if err != nil {
		t.Fatalf("touch missing mapping: %v", err)
	}
	if found || journeyID != "" {
		t.Fatalf("expected cache miss, got id=%q found=%t", journeyID, found)
	}

	storeIdentificationMapping(ctx, "failed", "N/A", now)
	journeyID, found, err = touchExistingIdentificationMapping(ctx, "failed", now.Add(time.Second))
	if err != nil {
		t.Fatalf("touch suppressed mapping: %v", err)
	}
	if !found || journeyID != "" {
		t.Fatalf("expected suppressed mapping, got id=%q found=%t", journeyID, found)
	}
}
