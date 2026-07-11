package gtfs

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestLoadShapeTracksImportsAndOrdersShapePoints(t *testing.T) {
	shapePath := filepath.Join(t.TempDir(), "shapes.txt")
	err := os.WriteFile(shapePath, []byte(`shape_id,shape_pt_lat,shape_pt_lon,shape_pt_sequence
shape-a,51.501,-0.141,2
shape-b,53.480,-2.242,1
shape-a,51.500,-0.142,1
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}

	schedule := &Schedule{fileMap: map[string]string{"shapes.txt": shapePath}}
	tracks, err := schedule.loadShapeTracks()
	if err != nil {
		t.Fatal(err)
	}

	if len(tracks) != 2 || len(tracks["shape-a"]) != 2 {
		t.Fatalf("got tracks %#v", tracks)
	}
	if got, want := tracks["shape-a"][0], (compactShapePoint{longitude: -0.142, latitude: 51.500}); got != want {
		t.Fatalf("first shape-a point = %v, want %v", got, want)
	}
	if _, err := os.Stat(shapePath); !os.IsNotExist(err) {
		t.Fatalf("shapes file was not removed after import: %v", err)
	}
}

func TestLoadShapeTracksAllowsFeedsWithoutShapes(t *testing.T) {
	tracks, err := (&Schedule{fileMap: map[string]string{}}).loadShapeTracks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 0 {
		t.Fatalf("got %d tracks, want none", len(tracks))
	}
}

func TestAssignJourneyPathTracksSplitsAtStops(t *testing.T) {
	track := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.01, 51}},
		{Type: "Point", Coordinates: []float64{0.02, 51}},
	}
	path := []*ctdf.JourneyPathItem{{}, {}}
	stopTimes := []StopTime{{StopID: "a"}, {StopID: "b"}, {StopID: "c"}}
	stops := map[string]ctdf.Location{
		"a": {Type: "Point", Coordinates: []float64{0, 51}},
		"b": {Type: "Point", Coordinates: []float64{0.01, 51}},
		"c": {Type: "Point", Coordinates: []float64{0.02, 51}},
	}

	if !assignJourneyPathTracks(path, stopTimes, stops, track) {
		t.Fatal("expected path tracks to be assigned")
	}
	if got := path[0].Track; len(got) < 2 || got[0].Coordinates[0] != 0 || got[len(got)-1].Coordinates[0] <= 0.01 {
		t.Fatalf("first leg = %#v", got)
	}
	if got := path[1].Track; len(got) < 2 || got[0].Coordinates[0] >= 0.01 || got[len(got)-1].Coordinates[0] != 0.02 {
		t.Fatalf("second leg = %#v", got)
	}
}

func TestAssignJourneyPathTracksFallsBackForDistantStop(t *testing.T) {
	track := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.01, 51}},
	}
	path := []*ctdf.JourneyPathItem{{}}
	stopTimes := []StopTime{{StopID: "near"}, {StopID: "far"}}
	stops := map[string]ctdf.Location{
		"near": {Type: "Point", Coordinates: []float64{0, 51}},
		"far":  {Type: "Point", Coordinates: []float64{1, 52}},
	}

	if assignJourneyPathTracks(path, stopTimes, stops, track) {
		t.Fatal("expected distant stop to leave the global journey track as fallback")
	}
	if len(path[0].Track) != 0 {
		t.Fatalf("got fallback path track %#v", path[0].Track)
	}
}

func TestAssignJourneyPathTracksAddsLeewayEitherSideOfCut(t *testing.T) {
	track := []ctdf.Location{
		{Type: "Point", Coordinates: []float64{0, 51}},
		{Type: "Point", Coordinates: []float64{0.0001, 51}},
		{Type: "Point", Coordinates: []float64{0.0002, 51}},
	}
	path := []*ctdf.JourneyPathItem{{}, {}}
	stopTimes := []StopTime{{StopID: "a"}, {StopID: "b"}, {StopID: "c"}}
	stops := map[string]ctdf.Location{
		"a": {Type: "Point", Coordinates: []float64{0, 51}},
		"b": {Type: "Point", Coordinates: []float64{0.0001, 51}},
		"c": {Type: "Point", Coordinates: []float64{0.0002, 51}},
	}

	if !assignJourneyPathTracks(path, stopTimes, stops, track) {
		t.Fatal("expected path tracks to be assigned")
	}
	if path[0].Track[len(path[0].Track)-1].Coordinates[0] <= 0.0001 {
		t.Fatalf("first leg did not extend after cut: %#v", path[0].Track)
	}
	if path[1].Track[0].Coordinates[0] >= 0.0001 {
		t.Fatalf("second leg did not extend before cut: %#v", path[1].Track)
	}
}
