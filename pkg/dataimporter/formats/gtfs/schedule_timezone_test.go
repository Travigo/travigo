package gtfs

import "testing"

func TestGTFSStopTimezone(t *testing.T) {
	stops := map[string]Stop{
		"station":       {Timezone: "Europe/Paris"},
		"platform":      {Parent: "station", Timezone: "Europe/London"},
		"empty-station": {},
		"cycle-a":       {Parent: "cycle-b"},
		"cycle-b":       {Parent: "cycle-a"},
	}
	tests := []struct {
		name string
		stop Stop
		want string
	}{
		{"own timezone", Stop{Timezone: "Europe/Berlin"}, "Europe/Berlin"},
		{"agency fallback", Stop{}, "Europe/Amsterdam"},
		{"parent overrides child", Stop{Parent: "station", Timezone: "UTC"}, "Europe/Paris"},
		{"nested parent", Stop{Parent: "platform"}, "Europe/Paris"},
		{"empty parent", Stop{Parent: "empty-station"}, "Europe/Amsterdam"},
		{"missing parent", Stop{Parent: "missing"}, "Europe/Amsterdam"},
		{"parent cycle", Stop{Parent: "cycle-a"}, "Europe/Amsterdam"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := gtfsStopTimezone(tt.stop, stops, "Europe/Amsterdam"); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
