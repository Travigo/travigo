package nyct

import "testing"

func TestTripKey(t *testing.T) {
	for _, tt := range []struct{ id, want string }{
		{"A20111204SAT_021150_2..N08R", "021150_2..N"},
		{"schedule_with_underscores_021150_2..N08R", "021150_2..N"},
		{"021150_2..N08R", "021150_2..N"},
		{"021150_2..N", "021150_2..N"},
		{"021150_2..N99", "021150_2..N"},
		{"021150_2..S08R", "021150_2..S"},
		{"021151_2..N08R", "021151_2..N"},
		{"-0000200_2..N08R", "-0000200_2..N"},
		{"00145000_2..N08R", "00145000_2..N"},
		{"040550_SI..S03R", "040550_SI..S"},
		{"", ""}, {"garbage", ""}, {"021150_2..E08R", ""},
		{"021150_2", ""}, {"021150_2..N08R_extra", ""},
	} {
		t.Run(tt.id, func(t *testing.T) {
			if got := TripKey(tt.id); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTripIDPreservesPathsAndShuttles(t *testing.T) {
	for _, tt := range []struct{ id, full, short string }{
		{"A20111204SAT_021150_2..N08R", "021150_2..N08R", "021150_2..N"},
		{"095950_2..N06R", "095950_2..N06R", "095950_2..N"},
		{"095950_2..N14R", "095950_2..N14R", "095950_2..N"},
		{"schedule_021150_GS.N01R", "021150_GS.N01R", "021150_GS.N"},
		{"021150_GS.S01R", "021150_GS.S01R", "021150_GS.S"},
		{"021150_FS.N01R", "021150_FS.N01R", "021150_FS.N"},
		{"021150_FS.S01R", "021150_FS.S01R", "021150_FS.S"},
		{"021150_FS.S", "021150_FS.S", "021150_FS.S"},
		{"bad", "", ""},
	} {
		if got := TripID(tt.id); got != tt.full {
			t.Errorf("TripID(%q)=%q, want %q", tt.id, got, tt.full)
		}
		if got := TripKey(tt.id); got != tt.short {
			t.Errorf("TripKey(%q)=%q, want %q", tt.id, got, tt.short)
		}
	}
}
