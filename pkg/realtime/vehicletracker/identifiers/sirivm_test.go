package identifiers

import "testing"

func TestSameServiceNameNormalizesCommonPresentationDifferences(t *testing.T) {
	for _, test := range []struct{ left, right string }{
		{" X 1 ", "x-1"},
		{"A/12", "a12"},
		{"25", "Route 25"},
	} {
		if !sameServiceName(test.left, test.right) {
			t.Fatalf("%q and %q should match", test.left, test.right)
		}
	}
}

func TestSameServiceNameDoesNotMatchDifferentRoutes(t *testing.T) {
	if sameServiceName("25", "26") {
		t.Fatal("different service names matched")
	}
}
