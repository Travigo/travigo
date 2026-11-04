package gtfs

import (
	"github.com/travigo/travigo/pkg/dataimporter/formats/gtfs/nyct"
	"testing"
)

func TestSubwayRealtimeIdentity(t *testing.T) {
	first := realtimeTripLocalID("ace-feed", nyct.SubwayDataset, "2026-11-04", "021150_2..N08R")
	changed := realtimeTripLocalID("other-feed", nyct.SubwayDataset, "2026-11-04", "021150_2..N08R")
	if first != changed {
		t.Fatal("feed change split the run identity")
	}
	for _, other := range []string{
		realtimeTripLocalID("ace-feed", nyct.SubwayDataset, "2026-11-04", "021150_2..N14R"),
		realtimeTripLocalID("ace-feed", nyct.SubwayDataset, "2026-11-04", "021150_2..N"),
		realtimeTripLocalID("ace-feed", nyct.SubwayDataset, "2026-11-05", "021150_2..N"),
		realtimeTripLocalID("ace-feed", nyct.SubwayDataset, "2026-11-04", "021150_2..S"),
		realtimeTripLocalID("ace-feed", nyct.SubwayDataset, "2026-11-04", "021151_2..N"),
	} {
		if other == first {
			t.Fatal("distinct runs share identity")
		}
	}
	if got := realtimeTripLocalID("feed", "ordinary", "2026-11-04", "021150_2..N08R"); got != "feed-realtime-2026-11-04-021150_2..N08R" {
		t.Fatal(got)
	}
}
