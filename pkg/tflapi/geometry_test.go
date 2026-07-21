package tflapi

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestDecodeLineString(t *testing.T) {
	track, err := DecodeLineString(`[[[-0.2,51.5],[-0.1,51.6],[-0.1,51.6],[0,51.7]]]`)
	if err != nil {
		t.Fatalf("decode line string: %v", err)
	}
	if got, want := len(track), 3; got != want {
		t.Fatalf("track points = %d, want %d", got, want)
	}
}

func TestSplitTrackCutsLegsWithOverlap(t *testing.T) {
	track := []ctdf.Location{testPoint(0, 51.5), testPoint(0.01, 51.5), testPoint(0.02, 51.5)}
	stops := []ctdf.Location{testPoint(0, 51.5), testPoint(0.01, 51.5), testPoint(0.02, 51.5)}
	legs, err := SplitTrack(stops, track)
	if err != nil {
		t.Fatalf("split track: %v", err)
	}
	if got, want := len(legs), 2; got != want {
		t.Fatalf("leg count = %d, want %d", got, want)
	}
	if legs[0][len(legs[0])-1].Coordinates[0] <= 0.01 || legs[1][0].Coordinates[0] >= 0.01 {
		t.Fatalf("legs do not overlap around the middle stop: %#v", legs)
	}
}

func TestSplitTrackSupportsRepeatedTerminal(t *testing.T) {
	track := []ctdf.Location{testPoint(0, 51.5), testPoint(0.01, 51.5), testPoint(0.01, 51.51), testPoint(0, 51.51), testPoint(0, 51.5)}
	stops := []ctdf.Location{testPoint(0, 51.5), testPoint(0.01, 51.5), testPoint(0, 51.51), testPoint(0, 51.5)}
	legs, err := SplitTrack(stops, track)
	if err != nil {
		t.Fatalf("split circular track: %v", err)
	}
	if got, want := len(legs), 3; got != want {
		t.Fatalf("leg count = %d, want %d", got, want)
	}
}

func TestSplitBestTrackDoesNotAssumeTfLRouteArrayOrder(t *testing.T) {
	wrongTrack := []ctdf.Location{testPoint(0, 51.5), testPoint(0.01, 51.5)}
	matchingTrack := []ctdf.Location{testPoint(1, 52.5), testPoint(1.01, 52.5), testPoint(1.02, 52.5)}
	stops := []ctdf.Location{testPoint(1, 52.5), testPoint(1.01, 52.5), testPoint(1.02, 52.5)}

	legs, trackIndex, err := SplitBestTrack(stops, [][]ctdf.Location{wrongTrack, matchingTrack})
	if err != nil {
		t.Fatal(err)
	}
	if trackIndex != 1 {
		t.Fatalf("selected track index = %d, want 1", trackIndex)
	}
	if len(legs) != 2 {
		t.Fatalf("leg count = %d, want 2", len(legs))
	}
}

func testPoint(longitude, latitude float64) ctdf.Location {
	return ctdf.Location{Type: "Point", Coordinates: []float64{longitude, latitude}}
}
