package ctdf

import (
	"testing"

	"github.com/liip/sheriff"
)

func TestJourneyTrackDataSourceIsExposedWithTrack(t *testing.T) {
	source := &DataSourceReference{
		OriginalFormat: "tfl-route-tracks",
		ProviderName:   "Transport for London",
		ProviderID:     "gb-tfl",
		DatasetID:      "gb-tfl-route-tracks",
		Timestamp:      "2026-07-21T12:00:00Z",
	}
	journey := &Journey{
		Track:           []Location{{Type: "Point", Coordinates: []float64{-0.1, 51.5}}},
		TrackDataSource: source,
		Path: []*JourneyPathItem{{
			Track:           []Location{{Type: "Point", Coordinates: []float64{-0.2, 51.6}}},
			TrackDataSource: source,
		}},
	}

	reduced, err := sheriff.Marshal(&sheriff.Options{Groups: []string{"basic", "detailed"}}, journey)
	if err != nil {
		t.Fatal(err)
	}
	value := reduced.(map[string]interface{})
	assertPublicTrackDataSource(t, value["TrackDataSource"])
	path := value["Path"].([]interface{})[0].(map[string]interface{})
	assertPublicTrackDataSource(t, path["TrackDataSource"])
}

func TestApplyJourneyTracksIncludesDataSource(t *testing.T) {
	journeySource := &DataSourceReference{DatasetID: "journey-track-source"}
	legSource := &DataSourceReference{DatasetID: "leg-track-source"}
	journey := &Journey{
		TrackRef: "whole",
		Path:     []*JourneyPathItem{{TrackRef: "leg"}},
	}
	applyJourneyTracks(journey, map[string]JourneyTrack{
		"whole": {Track: []Location{{Type: "Point"}}, DataSource: journeySource},
		"leg":   {Track: []Location{{Type: "Point"}}, DataSource: legSource},
	})

	if len(journey.Track) != 1 || journey.TrackDataSource != journeySource {
		t.Fatalf("journey track was not hydrated with its datasource: %#v", journey)
	}
	if len(journey.Path[0].Track) != 1 || journey.Path[0].TrackDataSource != legSource {
		t.Fatalf("path track was not hydrated with its datasource: %#v", journey.Path[0])
	}
}

func assertPublicTrackDataSource(t *testing.T, value interface{}) {
	t.Helper()
	source := value.(map[string]interface{})
	if source["ProviderName"] != "Transport for London" || source["ProviderID"] != "gb-tfl" || source["DatasetID"] != "gb-tfl-route-tracks" {
		t.Fatalf("unexpected track datasource: %#v", source)
	}
	if _, ok := source["OriginalFormat"]; ok {
		t.Fatal("internal OriginalFormat was exposed")
	}
	if _, ok := source["Timestamp"]; ok {
		t.Fatal("internal Timestamp was exposed")
	}
}
