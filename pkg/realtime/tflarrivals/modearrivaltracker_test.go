package tflarrivals

import (
	"net/url"
	"testing"
)

func TestModeArrivalsURLRequestsAllPredictionsPerStop(t *testing.T) {
	requestURL, err := url.Parse(modeArrivalsURL("tube", "test-key"))
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}

	if got, want := requestURL.Path, "/mode/tube/arrivals"; got != want {
		t.Errorf("path = %q, want %q", got, want)
	}
	if got, want := requestURL.Query().Get("count"), "-1"; got != want {
		t.Errorf("count = %q, want %q", got, want)
	}
	if got, want := requestURL.Query().Get("app_key"), "test-key"; got != want {
		t.Errorf("app_key = %q, want %q", got, want)
	}
}

func TestModeArrivalsURLLimitsBusPredictionsPerStop(t *testing.T) {
	requestURL, err := url.Parse(modeArrivalsURL("bus", "test-key"))
	if err != nil {
		t.Fatalf("parse request URL: %v", err)
	}

	if got, want := requestURL.Query().Get("count"), "10"; got != want {
		t.Errorf("count = %q, want %q", got, want)
	}
}
