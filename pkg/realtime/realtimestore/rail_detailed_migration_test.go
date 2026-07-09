package realtimestore

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"
)

func TestMigrateLegacyRailDetailedAllocationsGroupsLinxUnitsAndPreservesTTL(t *testing.T) {
	server := setupMemoryTestRedis(t)
	ctx := context.Background()
	key := realtimeJourneyRailDetailedKey("allocation", "legacy-linx")
	legacy := legacyJourneyDetailedRail{
		VehicleType: "gb-railclass-156",
		TrainLength: 3,
		VehicleIDs:  []string{"156406", "150234"},
		Carriages: []legacyRailCarriage{
			{
				ID:              "156406:52406",
				CarriageType:    "156",
				Class:           ctdf.JourneyDetailedRailSeatingStandard,
				CarriageID:      "52406",
				VehicleID:       "156406",
				VehiclePosition: 1,
				FleetID:         "156",
				SpecificType:    "DMSL",
				Livery:          "AT",
				Occupancy:       -1,
			},
			{
				ID:              "156406:57406",
				CarriageType:    "156",
				CarriageID:      "57406",
				VehicleID:       "156406",
				VehiclePosition: 2,
				FleetID:         "156",
				SpecificType:    "DMS",
				Occupancy:       -1,
			},
			{
				ID:              "150234:57234",
				CarriageType:    "150/2",
				CarriageID:      "57234",
				VehicleID:       "150234",
				VehiclePosition: 1,
				FleetID:         "150/2",
				SpecificType:    "DMS",
				Occupancy:       -1,
			},
		},
	}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := redis_client.Client.Set(ctx, key, legacyJSON, 2*time.Hour).Err(); err != nil {
		t.Fatal(err)
	}

	stats, err := MigrateLegacyRailDetailedAllocations(ctx)
	if err != nil {
		t.Fatalf("migrate allocations: %v", err)
	}
	if stats.Scanned != 1 || stats.Migrated != 1 || stats.Skipped != 0 || stats.Failed != 0 {
		t.Fatalf("unexpected migration stats: %+v", stats)
	}
	if ttl := server.TTL(key); ttl != 2*time.Hour {
		t.Fatalf("expected TTL to be preserved, got %s", ttl)
	}

	var migrated ctdf.JourneyDetailedRail
	migratedJSON, err := redis_client.Client.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(migratedJSON, &migrated); err != nil {
		t.Fatal(err)
	}
	if len(migrated.Trains) != 2 {
		t.Fatalf("expected two LINX units, got %+v", migrated.Trains)
	}
	if migrated.Trains[0].ID != "156406" || migrated.Trains[0].FleetID != "156" || migrated.Trains[0].TrainLength != 2 {
		t.Fatalf("unexpected first migrated train: %+v", migrated.Trains[0])
	}
	firstCarriage := migrated.Trains[0].Carriages[0]
	if firstCarriage.ID != "156406:52406" || firstCarriage.VehicleID != "52406" {
		t.Fatalf("expected LINX carriage identity to be migrated, got %+v", firstCarriage)
	}
	if firstCarriage.CarriageType != "DMSL" || firstCarriage.SeatingClass != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("expected vehicle and seating classes to remain distinct, got %+v", firstCarriage)
	}

	stats, err = MigrateLegacyRailDetailedAllocations(ctx)
	if err != nil {
		t.Fatalf("rerun migration: %v", err)
	}
	if stats.Migrated != 0 || stats.Skipped != 1 || stats.Failed != 0 {
		t.Fatalf("expected migration to be idempotent, got %+v", stats)
	}
}

func TestConvertLegacyRailDetailedAllocationGroupsDarwinFormations(t *testing.T) {
	legacyJSON, err := json.Marshal(legacyJourneyDetailedRail{
		Carriages: []legacyRailCarriage{
			{ID: "front:A", CarriageType: "first", Occupancy: -1},
			{ID: "rear:A", CarriageType: "standard", Occupancy: -1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	migrated, legacy, err := convertLegacyRailDetailedAllocation(legacyJSON)
	if err != nil {
		t.Fatalf("convert allocation: %v", err)
	}
	if !legacy {
		t.Fatal("expected legacy allocation to be detected")
	}
	if len(migrated.Trains) != 2 || migrated.Trains[0].ID != "front" || migrated.Trains[1].ID != "rear" {
		t.Fatalf("expected Darwin formations to become separate trains, got %+v", migrated.Trains)
	}
	if migrated.Trains[0].Carriages[0].ID != "A" ||
		migrated.Trains[0].Carriages[0].SeatingClass != ctdf.JourneyDetailedRailSeatingFirst {
		t.Fatalf("unexpected front formation carriage: %+v", migrated.Trains[0].Carriages[0])
	}
	if migrated.Trains[1].Carriages[0].ID != "A" ||
		migrated.Trains[1].Carriages[0].SeatingClass != ctdf.JourneyDetailedRailSeatingStandard {
		t.Fatalf("unexpected rear formation carriage: %+v", migrated.Trains[1].Carriages[0])
	}
}

func TestConvertLegacyRailDetailedAllocationSkipsNewFormat(t *testing.T) {
	newJSON, err := json.Marshal(ctdf.JourneyDetailedRail{
		Trains: []ctdf.RailTrain{{ID: "unit-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, legacy, err := convertLegacyRailDetailedAllocation(newJSON)
	if err != nil {
		t.Fatalf("inspect allocation: %v", err)
	}
	if legacy {
		t.Fatal("expected new allocation format to be skipped")
	}
}
