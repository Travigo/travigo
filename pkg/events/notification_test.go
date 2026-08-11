package events

import (
	"encoding/json"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestGetNotificationDataNextStopChanged(t *testing.T) {
	bodyBytes, err := json.Marshal(ctdf.RealtimeJourney{
		Journey:     &ctdf.Journey{DestinationDisplay: "London"},
		NextStopRef: "stop-b",
		NextStop:    &ctdf.Stop{PrimaryName: "Cambridge"},
	})
	if err != nil {
		t.Fatalf("marshal event body: %v", err)
	}

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal event body: %v", err)
	}

	data := GetNotificationData(&ctdf.Event{
		Type: ctdf.EventTypeRealtimeJourneyNextStopChanged,
		Body: body,
	})

	if data.Title != "Next stop changed" {
		t.Fatalf("notification title = %q, want %q", data.Title, "Next stop changed")
	}
	if data.Message != "The service to London is now heading to Cambridge." {
		t.Fatalf("notification message = %q", data.Message)
	}
}
