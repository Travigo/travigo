package netex

import (
	"encoding/xml"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func xmlName(local string) xml.Name        { return xml.Name{Local: local} }
func xmlAttr(local, value string) xml.Attr { return xml.Attr{Name: xmlName(local), Value: value} }

func TestScheduleAvailabilityConstrainsWeekdaysAndExclusionsPerDayType(t *testing.T) {
	data := &scheduleData{
		dayTypes: map[string]*xmlNode{
			"weekend": {
				Name:     xmlName("DayType"),
				Children: []*xmlNode{{Name: xmlName("properties"), Children: []*xmlNode{{Name: xmlName("PropertyOfDay"), Children: []*xmlNode{{Name: xmlName("DaysOfWeek"), Text: "Saturday"}}}}}},
			},
			"special": {Name: xmlName("DayType")},
		},
		operatingPeriods: map[string]*xmlNode{
			"weekend-period": {Name: xmlName("OperatingPeriod"), Children: []*xmlNode{{Name: xmlName("FromDate"), Text: "2026-06-01"}, {Name: xmlName("ToDate"), Text: "2026-06-30"}}},
		},
		assignments: []*xmlNode{
			{Name: xmlName("DayTypeAssignment"), Attrs: []xml.Attr{xmlAttr("id", "weekend-positive")}, Children: []*xmlNode{{Name: xmlName("OperatingPeriodRef"), Attrs: []xml.Attr{xmlAttr("ref", "weekend-period")}}, {Name: xmlName("DayTypeRef"), Attrs: []xml.Attr{xmlAttr("ref", "weekend")}}}},
			{Name: xmlName("DayTypeAssignment"), Attrs: []xml.Attr{xmlAttr("id", "weekend-exception")}, Children: []*xmlNode{{Name: xmlName("Date"), Text: "2026-06-13"}, {Name: xmlName("DayTypeRef"), Attrs: []xml.Attr{xmlAttr("ref", "weekend")}}, {Name: xmlName("IsAvailable"), Text: "false"}}},
			{Name: xmlName("DayTypeAssignment"), Attrs: []xml.Attr{xmlAttr("id", "special-positive")}, Children: []*xmlNode{{Name: xmlName("Date"), Text: "2026-06-13"}, {Name: xmlName("DayTypeRef"), Attrs: []xml.Attr{xmlAttr("ref", "special")}}}},
		},
	}
	data.assignmentsByDayType = map[string][]*xmlNode{
		"weekend": {data.assignments[0], data.assignments[1]},
		"special": {data.assignments[2]},
	}
	availability, err := data.availability([]string{"weekend", "special"})
	if err != nil {
		t.Fatal(err)
	}
	for date, want := range map[string]bool{
		"2026-06-06": true,
		"2026-06-07": false,
		"2026-06-13": true, // Excluded from weekend, but included by the other DayType.
		"2026-06-20": true,
		"2026-07-04": false,
	} {
		parsed, err := time.Parse(ctdf.YearMonthDayFormat, date)
		if err != nil {
			t.Fatal(err)
		}
		if got := availability.MatchDate(parsed); got != want {
			t.Errorf("MatchDate(%s) = %t, want %t", date, got, want)
		}
	}
}

func TestScheduleAvailabilityRejectsUnboundedOperatingPeriod(t *testing.T) {
	data := &scheduleData{
		dayTypes:         map[string]*xmlNode{"weekday": {Name: xmlName("DayType")}},
		operatingPeriods: map[string]*xmlNode{"open": {Name: xmlName("OperatingPeriod"), Children: []*xmlNode{{Name: xmlName("FromDate"), Text: "2026-01-01"}}}},
		assignments:      []*xmlNode{{Name: xmlName("DayTypeAssignment"), Children: []*xmlNode{{Name: xmlName("OperatingPeriodRef"), Attrs: []xml.Attr{xmlAttr("ref", "open")}}, {Name: xmlName("DayTypeRef"), Attrs: []xml.Attr{xmlAttr("ref", "weekday")}}}}},
	}
	if _, err := data.availability([]string{"weekday"}); err == nil {
		t.Fatal("expected open-ended schedule calendar to fail")
	}
}

func TestNetexPassingTimePreservesDayOffsetAndUTCClock(t *testing.T) {
	node := &xmlNode{Name: xmlName("TimetabledPassingTime"), Children: []*xmlNode{{Name: xmlName("ArrivalTime"), Text: "01:10:00"}, {Name: xmlName("ArrivalDayOffset"), Text: "1"}}}
	parsed, err := netexPassingTime(node, "ArrivalTime", "ArrivalDayOffset", time.FixedZone("test", 3600))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Day() != 2 || parsed.Hour() != 1 || parsed.Location() != time.UTC {
		t.Fatalf("unexpected normalized schedule time: %s (%s)", parsed, parsed.Location())
	}
}
