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
