package journeytracks

import "testing"

func TestMatchStopsSupportsExpressAndRepeatedStops(t *testing.T) {
	route := [][]string{{"a"}, {"loop"}, {"b"}, {"loop"}, {"c"}}
	positions, ok := MatchStops([]string{"a", "b", "loop", "c"}, route)
	if !ok {
		t.Fatal("expected ordered subsequence to match")
	}
	want := []int{0, 2, 3, 4}
	for index := range want {
		if positions[index] != want[index] {
			t.Fatalf("positions = %v, want %v", positions, want)
		}
	}
	if _, ok := MatchStops([]string{"b", "a"}, route); ok {
		t.Fatal("expected reversed journey not to match")
	}
}
