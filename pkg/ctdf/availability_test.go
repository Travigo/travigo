package ctdf

import (
	"testing"
	"time"
)

func TestAvailabilityEmptyRuleDoesNotMatch(t *testing.T) {
	availability := &Availability{Match: []AvailabilityRule{{}}}
	if availability.MatchDate(time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)) {
		t.Fatal("empty availability rule unexpectedly matched")
	}
}
