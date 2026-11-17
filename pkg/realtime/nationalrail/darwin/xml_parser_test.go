package darwin

import (
	"strings"
	"testing"
	"time"
)

func TestParseXMLFileReadsPportTimestamp(t *testing.T) {
	const timestamp = "2026-11-17T10:20:30.1234567+00:00"

	data, err := ParseXMLFile(strings.NewReader(`<Pport ts="` + timestamp + `"><TS uid="1A23" ssd="2026-11-17" rid="rid-1"></TS></Pport>`))
	if err != nil {
		t.Fatalf("ParseXMLFile returned error: %v", err)
	}

	expected, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		t.Fatalf("test timestamp is invalid: %v", err)
	}
	if !data.GeneratedAt.Equal(expected) {
		t.Fatalf("GeneratedAt = %s, want %s", data.GeneratedAt, expected)
	}
}

func TestParseXMLFileWithoutPportTimestamp(t *testing.T) {
	data, err := ParseXMLFile(strings.NewReader(`<Pport><TS uid="1A23" ssd="2026-11-17" rid="rid-1"></TS></Pport>`))
	if err != nil {
		t.Fatalf("ParseXMLFile returned error: %v", err)
	}
	if !data.GeneratedAt.IsZero() {
		t.Fatalf("GeneratedAt = %s, want zero", data.GeneratedAt)
	}
}
