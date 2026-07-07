package routes

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestResolveStopDisplayNameUsesServiceOverride(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-primary",
		OtherIdentifiers:  []string{"stop-other"},
		PrimaryName:       "Original",
	}

	displayName, overrideApplied := resolveStopDisplayName(stop, map[string]string{
		"stop-other": "Overridden",
	})

	if displayName != "Overridden" {
		t.Fatalf("expected overridden display name, got %q", displayName)
	}
	if !overrideApplied {
		t.Fatal("expected override to be reported as applied")
	}
}

func TestResolveStopDisplayNameFallsBackToPrimaryName(t *testing.T) {
	stop := &ctdf.Stop{
		PrimaryIdentifier: "stop-primary",
		PrimaryName:       "Original",
	}

	displayName, overrideApplied := resolveStopDisplayName(stop, nil)

	if displayName != "Original" {
		t.Fatalf("expected primary display name, got %q", displayName)
	}
	if overrideApplied {
		t.Fatal("did not expect override to be reported as applied")
	}
}
