package datalinker

import (
	"context"
	"fmt"
	"maps"
	"math/rand/v2"
	"reflect"
	"slices"
	"testing"
)

// Apply actual per-origin replacements to yesterday's graph. This exercises
// the invalidation set as well as the transfer rules themselves.
func TestIncrementalTransferParityAcrossUpdates(t *testing.T) {
	config := StopTransferBuildConfig{MaxDistanceMetres: 500, MaxNearbyTransfersPerStop: 3, WorkerCount: 3}.withDefaults()
	random := rand.New(rand.NewPCG(49, 104))
	rows := map[string]transferSnapshot{}
	// Dense local stops exercise nearest-N cutoff changes. A second cluster
	// ensures the update stays local even when several feeds change together.
	for n := range 80 {
		lat := 52.0 + float64(n/40) + random.Float64()*.006
		id := fmt.Sprintf("s%02d", n)
		rows[id] = transferSnapshot{ID: id, HasService: true, Latitude: lat, Longitude: 0.12 + random.Float64()*.002}
	}
	rows["s00"] = withPlatform(rows["s00"], "s01")
	graph := referenceTransferGraph(rows, config)
	mutations := []func(map[string]transferSnapshot){
		func(r map[string]transferSnapshot) { s := r["s01"]; s.Latitude += .08; r[s.ID] = s },
		func(r map[string]transferSnapshot) { delete(r, "s00") },
		func(r map[string]transferSnapshot) { s := r["s05"]; s.HasService = false; r[s.ID] = s },
		func(r map[string]transferSnapshot) {
			s := r["s07"]
			s.Associations = []string{"shared"}
			r[s.ID] = s
			s = r["s08"]
			s.Associations = []string{"shared"}
			r[s.ID] = s
		},
		func(r map[string]transferSnapshot) {
			s := r["s05"]
			s.HasService = true
			r[s.ID] = s
			r["s12"] = withPlatform(r["s12"], "s79")
		},
		func(r map[string]transferSnapshot) { delete(r, "s79"); s := r["s12"]; s.Platforms = nil; r[s.ID] = s },
		func(r map[string]transferSnapshot) {
			s := r["s03"]
			s.ID = "canonical-replacement"
			delete(r, "s03")
			r[s.ID] = s
		},
	}
	for step, mutate := range mutations {
		old := maps.Clone(rows)
		mutate(rows)
		changed := changedTransferInputs(old, rows, nil, true)
		index, affected, err := planTransferOrigins(context.Background(), config, old, rows, changed, map[string]bool{}, true, false, false)
		if err != nil {
			t.Fatal(err)
		}
		if step == 0 && affected["s65"] {
			t.Fatal("move dirtied an unrelated cluster")
		}
		for k := range graph {
			if affected[k.from] {
				delete(graph, k)
			}
		}
		for batch := range slices.Chunk(slices.Sorted(maps.Keys(affected)), 2) {
			maps.Copy(graph, index.edges(batch))
		}
		want := referenceTransferGraph(rows, config)
		if !reflect.DeepEqual(graph, want) {
			for k, w := range want {
				if g, ok := graph[k]; !ok || g != w {
					t.Fatalf("step %d edge %v: got %+v, want %+v", step, k, g, w)
				}
			}
			t.Fatalf("step %d has unexpected extra edges", step)
		}
	}
}
func withPlatform(s transferSnapshot, id string) transferSnapshot {
	s.Platforms = []string{id}
	return s
}
func referenceTransferGraph(rows map[string]transferSnapshot, config StopTransferBuildConfig) map[transferKey]transferCandidate {
	var stops []*transferStop
	for _, id := range slices.Sorted(maps.Keys(rows)) {
		stops = append(stops, rows[id].stop(len(stops)))
	}
	graph := map[transferKey]transferCandidate{}
	buildSameStopGroupTransfers(stops, graph, config)
	buildPlatformAliasTransfers(stops, graph, config)
	buildNearbyWalkTransfers(stops, graph, config, true)
	return graph
}

// Compare the same indexed network with every origin selected and with only
// the origins affected by one platform's relocation. Mongo I/O is excluded.
func BenchmarkTransferEdgeSelection(b *testing.B) {
	config := StopTransferBuildConfig{WorkerCount: 2}.withDefaults()
	const count = 8192
	rows := make([]*transferStop, 0, count)
	ids := make([]string, 0, count)
	for n := range count {
		id := fmt.Sprintf("stop-%05d", n)
		stop := testTransferStop(n, id, 52+float64(n/128)*.001, 0.12+float64(n%128)*.002)
		stop.HasService = true
		ids = append(ids, id)
		rows = append(rows, stop)
	}
	moved := rows[count/2]
	original := snapshotForStop(moved)
	moved.Longitude += .003
	moved.Location.Coordinates[0] = moved.Longitude
	ix := newTransferIndex(rows, config)
	affected := ix.affected(map[string]transferSnapshot{moved.PrimaryIdentifier: original}, map[string]bool{moved.PrimaryIdentifier: true})
	for _, tc := range []struct {
		name string
		ids  []string
	}{{"all", ids}, {"one_move", slices.Sorted(maps.Keys(affected))}} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for batch := range slices.Chunk(tc.ids, 128) {
					ix.edges(batch)
				}
			}
			b.ReportMetric(float64(len(tc.ids)), "origins/op")
		})
	}
}
