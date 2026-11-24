package datalinker

import (
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestTransferIndexEdgesMatchFullBuilder(t *testing.T) {
	config := StopTransferBuildConfig{MaxDistanceMetres: 500, MinChangeSeconds: 60, WalkSpeedMetresPerSec: 1.3, MaxNearbyTransfersPerStop: 3, SameStopGroupMultiplier: 2, WorkerCount: 1}.withDefaults()
	stops := []*transferStop{
		testTransferStop(0, "a", 51, 0), testTransferStop(1, "b", 51.001, 0), testTransferStop(2, "c", 51.002, 0), testTransferStop(3, "d", 51.01, 0),
	}
	for _, s := range stops {
		s.HasService = true
	}
	stops[0].Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "g"}}
	stops[1].Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "g"}}
	stops[0].Platforms = []*ctdf.StopPlatform{{PrimaryIdentifier: "b"}}
	full := map[transferKey]transferCandidate{}
	buildSameStopGroupTransfers(stops, full, config)
	buildPlatformAliasTransfers(stops, full, config)
	buildNearbyWalkTransfers(stops, full, config, true)
	origins := []string{"a", "b", "c", "d"}
	incremental := newTransferIndex(stops, config).edges(origins)
	if !reflect.DeepEqual(full, incremental) {
		t.Fatalf("incremental edges differ from full builder\nfull=%#v\nincremental=%#v", full, incremental)
	}
	for _, batch := range [][]string{{"b"}, {"c", "d"}, {"a", "c"}} {
		got := newTransferIndex(stops, config).edges(batch)
		want := map[transferKey]transferCandidate{}
		for k, v := range full {
			for _, origin := range batch {
				if k.from == origin {
					want[k] = v
					break
				}
			}
		}
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("batch %v differs\nwant=%#v\ngot=%#v", batch, want, got)
		}
	}
}

func TestSnapshotForStopCanonicalizesRelationships(t *testing.T) {
	a := testTransferStop(0, "a", 51, 0)
	a.Associations = []*ctdf.Association{{Type: "stop_group", AssociatedIdentifier: "z"}, {AssociatedIdentifier: "g"}, {AssociatedIdentifier: "z"}}
	a.Platforms = []*ctdf.StopPlatform{{PrimaryIdentifier: "p2"}, {PrimaryIdentifier: "p1"}, {PrimaryIdentifier: "p1"}}
	s := snapshotForStop(a)
	if !reflect.DeepEqual(s.Associations, []string{"g", "z"}) || !reflect.DeepEqual(s.Platforms, []string{"p1", "p2"}) {
		t.Fatalf("snapshot not canonical: %#v", s)
	}
	if s.Tile != transferSnapshotTile(a.Latitude, a.Longitude) {
		t.Fatalf("unexpected snapshot tile %q", s.Tile)
	}
}
