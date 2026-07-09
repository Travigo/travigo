package darwin

import (
	"strings"
	"testing"
)

func TestParseSchedulePreservesRepeatedLocationsInDocumentOrder(t *testing.T) {
	xmlMessage := `
		<Pport>
			<uR>
				<schedule rid="202607096793377" uid="C93377" ssd="2026-07-09" toc="GN">
					<OR tpl="ELYY" ptd="11:51" can="true" />
					<IP tpl="WTRBECH" pta="12:01" ptd="12:01" can="true" />
					<IP tpl="CAMBNTH" pta="12:06" ptd="12:06" can="true" />
					<IP tpl="CAMBDGE" pta="12:12" ptd="12:12" can="true" />
					<OR tpl="CAMBDGE" ptd="12:12" can="false" />
					<IP tpl="CAMBS" pta="12:16" ptd="12:16" />
					<DT tpl="KNGX" pta="13:02" />
					<cancelReason>887</cancelReason>
				</schedule>
			</uR>
		</Pport>`

	pushPortData, err := ParseXMLFile(strings.NewReader(xmlMessage))
	if err != nil {
		t.Fatalf("expected schedule XML to parse: %v", err)
	}
	if len(pushPortData.Schedules) != 1 {
		t.Fatalf("expected one schedule, got %d", len(pushPortData.Schedules))
	}

	schedule := pushPortData.Schedules[0]
	if schedule.CancelReason != "887" {
		t.Fatalf("expected cancellation reason 887, got %q", schedule.CancelReason)
	}

	expectedLocations := []struct {
		locationType string
		tiploc       string
		cancelled    string
	}{
		{"OR", "ELYY", "true"},
		{"IP", "WTRBECH", "true"},
		{"IP", "CAMBNTH", "true"},
		{"IP", "CAMBDGE", "true"},
		{"OR", "CAMBDGE", "false"},
		{"IP", "CAMBS", ""},
		{"DT", "KNGX", ""},
	}

	if len(schedule.Locations) != len(expectedLocations) {
		t.Fatalf("expected %d locations, got %d", len(expectedLocations), len(schedule.Locations))
	}

	for index, expected := range expectedLocations {
		actual := schedule.Locations[index]
		if actual.Type != expected.locationType ||
			actual.Tiploc != expected.tiploc ||
			actual.Cancelled != expected.cancelled {
			t.Fatalf(
				"location %d: expected type=%q tiploc=%q cancelled=%q, got type=%q tiploc=%q cancelled=%q",
				index,
				expected.locationType,
				expected.tiploc,
				expected.cancelled,
				actual.Type,
				actual.Tiploc,
				actual.Cancelled,
			)
		}
	}
}

func TestParseScheduleIncludesOperationalAndPassingLocations(t *testing.T) {
	xmlMessage := `
		<Pport>
			<uR>
				<schedule rid="rid" uid="uid" ssd="2026-07-09" toc="GN">
					<OPOR tpl="ORIGIN" />
					<OPIP tpl="INTER" />
					<PP tpl="PASS" />
					<OPDT tpl="DEST" />
				</schedule>
			</uR>
		</Pport>`

	pushPortData, err := ParseXMLFile(strings.NewReader(xmlMessage))
	if err != nil {
		t.Fatalf("expected schedule XML to parse: %v", err)
	}

	schedule := pushPortData.Schedules[0]
	expectedTypes := []string{"OPOR", "OPIP", "PP", "OPDT"}
	for index, expectedType := range expectedTypes {
		if schedule.Locations[index].Type != expectedType {
			t.Fatalf("location %d: expected type %q, got %q", index, expectedType, schedule.Locations[index].Type)
		}
	}
}
