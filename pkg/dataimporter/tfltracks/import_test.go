package tfltracks

import (
	"testing"

	"github.com/travigo/travigo/pkg/tflapi"
)

func TestRouteIdentifierIncludesStopSequence(t *testing.T) {
	first := routeIdentifier("bus", "24", "outbound", []string{"a", "b"})
	second := routeIdentifier("bus", "24", "outbound", []string{"a", "b"})
	changed := routeIdentifier("bus", "24", "outbound", []string{"a", "c"})
	if first != second {
		t.Fatalf("stable route identifiers differ: %q != %q", first, second)
	}
	if first == changed {
		t.Fatalf("different stop sequences share route identifier %q", first)
	}
}

func TestLineKeysIncludeIDAndDisplayName(t *testing.T) {
	keys := lineKeys(tflapi.Line{ID: "great-western-railway", Name: "Great Western Railway"})
	if got, want := len(keys), 2; got != want {
		t.Fatalf("line key count = %d, want %d", got, want)
	}
}
