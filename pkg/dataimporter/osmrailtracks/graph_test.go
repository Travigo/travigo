package osmrailtracks

import (
	"reflect"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestRailWayFiltering(t *testing.T) {
	tests := []struct {
		tags map[string]string
		want bool
	}{
		{tags: map[string]string{"railway": "rail"}, want: true},
		{tags: map[string]string{"railway": "rail", "service": "crossover"}, want: true},
		{tags: map[string]string{"railway": "rail", "service": "siding"}, want: true},
		{tags: map[string]string{"railway": "rail", "access": "private"}, want: false},
		{tags: map[string]string{"railway": "light_rail"}, want: false},
	}
	for _, test := range tests {
		if got := isUsableRailWay(test.tags); got != test.want {
			t.Fatalf("isUsableRailWay(%v) = %t, want %t", test.tags, got, test.want)
		}
	}
}

func TestRailGraphRoutesAndSnapsStops(t *testing.T) {
	graph := buildRailGraph(
		[]railWay{
			{nodeIDs: []int64{1, 2, 3}},
			{nodeIDs: []int64{1, 4, 3}},
		},
		map[int64]point{
			1: {lon: -0.10, lat: 51.50},
			2: {lon: -0.09, lat: 51.50},
			3: {lon: -0.08, lat: 51.50},
			4: {lon: -0.09, lat: 51.52},
		},
	)
	from, _, found := graph.nearestNode(point{lon: -0.1001, lat: 51.50}, 100)
	if !found {
		t.Fatal("expected origin to snap to graph")
	}
	to, _, found := graph.nearestNode(point{lon: -0.0799, lat: 51.50}, 100)
	if !found {
		t.Fatal("expected destination to snap to graph")
	}

	track, err := graph.route(from, to)
	if err != nil {
		t.Fatal(err)
	}
	want := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{-0.10, 51.50}},
		{Type: "Point", Coordinates: []float64{-0.08, 51.50}},
	}
	if !reflect.DeepEqual(track, want) {
		t.Fatalf("route = %#v, want %#v", track, want)
	}
}

func TestSimplifyTrackKeepsMeaningfulCurve(t *testing.T) {
	track := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.001, 51.0002}},
		{Type: "Point", Coordinates: []float64{0.002, 51}},
	}
	if got := simplifyTrack(track, 5); len(got) != 3 {
		t.Fatalf("simplified curved track has %d points, want 3", len(got))
	}
}

func TestTrackIdentifiersAreDirectional(t *testing.T) {
	if trackIdentifier("a", "b") == trackIdentifier("b", "a") {
		t.Fatal("opposite track directions must have distinct identifiers")
	}
	if routeIdentifier([]string{"a", "b"}) != routeIdentifier([]string{"a", "b"}) {
		t.Fatal("route identifier is not deterministic")
	}
}

func TestRouteLegUsesPassingPoints(t *testing.T) {
	graph := buildRailGraph([]railWay{
		{nodeIDs: []int64{1, 2, 3}},
		{nodeIDs: []int64{1, 4, 3}},
	}, map[int64]point{
		1: {lon: -0.10, lat: 51.50},
		2: {lon: -0.09, lat: 51.50},
		3: {lon: -0.08, lat: 51.50},
		4: {lon: -0.09, lat: 51.52},
	})
	snap := func(p point) snappedStop {
		node, _, ok := graph.nearestNode(p, 100)
		if !ok {
			t.Fatalf("could not snap %v", p)
		}
		return snappedStop{graphNode: node}
	}
	origin := snap(point{lon: -0.10, lat: 51.50})
	destination := snap(point{lon: -0.08, lat: 51.50})
	snapped := map[string]snappedStop{"via": snap(point{lon: -0.09, lat: 51.52})}
	cache := map[[2]int]cachedTrack{}
	fallback, err := graph.routeLeg(origin, destination, []string{"unknown"}, snapped, cache)
	if err != nil || len(fallback) != 2 {
		t.Fatalf("direct fallback = %v, %v", fallback, err)
	}
	alternate, err := graph.routeLeg(origin, destination, []string{"unknown", "via"}, snapped, cache)
	if err != nil || len(alternate) != 3 || alternate[1].Coordinates[1] != 51.52 {
		t.Fatalf("alternate route = %v, %v", alternate, err)
	}
	stops := []string{"a", "b"}
	if routeIdentifier(stops, []string{"unknown"}) == routeIdentifier(stops, []string{"via"}) ||
		trackIdentifier("a", "b", "unknown") == trackIdentifier("a", "b", "via") {
		t.Fatal("alternate passing paths share an identifier")
	}
}

func TestRouteLegFallsBackWhenWaypointIsDisconnected(t *testing.T) {
	graph := buildRailGraph([]railWay{
		{nodeIDs: []int64{1, 2}},
		{nodeIDs: []int64{3, 4}},
	}, map[int64]point{
		1: {lon: 0.01, lat: 52},
		2: {lon: 0.02, lat: 52},
		3: {lon: 0.01, lat: 52.01},
		4: {lon: 0.02, lat: 52.01},
	})
	snap := func(lon, lat float64) snappedStop {
		node, _, ok := graph.nearestNode(point{lon: lon, lat: lat}, 100)
		if !ok {
			t.Fatal("expected test point to snap")
		}
		return snappedStop{graphNode: node}
	}
	origin, destination := snap(0.01, 52), snap(0.02, 52)
	snapped := map[string]snappedStop{"disconnected": snap(0.01, 52.01)}
	track, fallback, err := graph.routeLegWithFallback(origin, destination, []string{"disconnected"}, snapped, map[[2]int]cachedTrack{})
	if err != nil || !fallback || len(track) != 2 {
		t.Fatalf("expected direct track, got %v, fallback=%t, err=%v", track, fallback, err)
	}
}
