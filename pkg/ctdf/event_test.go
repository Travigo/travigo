package ctdf

import "testing"

func TestEventTypeValid(t *testing.T) {
	if !EventTypeRealtimeJourneyCancelled.Valid() {
		t.Fatal("expected known EventType to be valid")
	}
	if EventType("JourneyEdited").Valid() {
		t.Fatal("expected unknown EventType to be invalid")
	}
}
