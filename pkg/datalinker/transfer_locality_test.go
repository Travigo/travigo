package datalinker

import (
	"strings"
	"testing"
)

func TestTransferSnapshotTileBoundaries(t *testing.T) {
	if got, want := transferSnapshotTile(0, 0), "1800:3600"; got != want {
		t.Fatalf("origin tile = %q, want %q", got, want)
	}
	if got, want := transferSnapshotTile(-0.001, -0.001), "1799:3599"; got != want {
		t.Fatalf("negative boundary tile = %q, want %q", got, want)
	}
	if transferSnapshotTile(51.5, 181) != transferSnapshotTile(51.5, -179) {
		t.Fatal("equivalent date-line longitudes should share a tile")
	}
}

func TestTransferLocalityCandidatesDeduplicatesAndExpandsLongitudeAtHighLatitude(t *testing.T) {
	seed := transferSnapshot{ID: "seed", Latitude: 80, Longitude: 0, Associations: []string{"group"}, Platforms: []string{"platform"}}
	tiles, ids := transferLocalityCandidates([]transferSnapshot{seed, seed}, 1000)
	if len(ids) != 3 {
		t.Fatalf("reference IDs = %v, want deduplicated seed/group/platform", ids)
	}
	if len(tiles) < 3 {
		t.Fatalf("high latitude longitude expansion was too narrow: %d tiles", len(tiles))
	}
	seen := make(map[string]bool)
	for _, tile := range tiles {
		if seen[tile] || !strings.Contains(tile, ":") {
			t.Fatalf("invalid or duplicate tile %q", tile)
		}
		seen[tile] = true
	}
}

func TestTransferLocalityCandidatesFallsBackForExcessiveTileRange(t *testing.T) {
	tiles, ids := transferLocalityCandidates([]transferSnapshot{{Latitude: 0, Longitude: 0}}, 1000000)
	if tiles != nil || ids != nil {
		t.Fatalf("excessive range should request full scan, got %d tiles and %d IDs", len(tiles), len(ids))
	}
}
