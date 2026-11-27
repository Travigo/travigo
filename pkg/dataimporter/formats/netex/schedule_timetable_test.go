package netex

import (
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/dataimporter/datasets"
)

func TestScheduleDecoderRejectsUnsupportedJourneyVariants(t *testing.T) {
	decoder := xml.NewDecoder(strings.NewReader(`<TemplateServiceJourney id="template-1"/>`))
	token, err := decoder.Token()
	if err != nil {
		t.Fatal(err)
	}
	start, ok := token.(xml.StartElement)
	if !ok {
		t.Fatalf("token type = %T, want xml.StartElement", token)
	}
	var schedule Schedule
	consumed, err := schedule.scheduleData.decodeScheduleElement(decoder, start)
	if err != nil {
		t.Fatal(err)
	}
	if !consumed {
		t.Fatal("unsupported service journey variant was not recognized")
	}
	_, _, _, err = schedule.convertTimetable(datasets.DataSet{Identifier: "test"}, nil, time.Now())
	if err == nil || !strings.Contains(err.Error(), "TemplateServiceJourney") {
		t.Fatalf("conversion error = %v, want unsupported variant error", err)
	}
}

func TestScheduleRequiresTimezoneWhenJourneysExist(t *testing.T) {
	schedule := Schedule{scheduleData: scheduleData{journeys: map[string]*xmlNode{"journey": {Name: xmlName("ServiceJourney")}}}}
	_, _, _, err := schedule.convertTimetable(datasets.DataSet{Identifier: "test"}, nil, time.Now())
	if err == nil || !strings.Contains(err.Error(), "no timezone") {
		t.Fatalf("conversion error = %v, want missing timezone error", err)
	}
}
