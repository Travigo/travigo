package ctdf

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestUserNotificationSubscriptionJSONStoresSingleEventType(t *testing.T) {
	subscription := UserNotificationSubscription{
		PrimaryIdentifier: "subscription-1",
		UserID:            "user-1",
		EventType:         EventTypeRealtimeJourneyCancelled,
		Values: map[string]interface{}{
			"JourneyRef": "journey-1",
		},
	}

	data, err := json.Marshal(subscription)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	body := string(data)
	if !strings.Contains(body, `"eventType":"RealtimeJourneyCancelled"`) {
		t.Fatalf("json.Marshal() = %s, want singular eventType", body)
	}
	if strings.Contains(body, `"events"`) {
		t.Fatalf("json.Marshal() = %s, must not contain events array", body)
	}
	if !strings.Contains(body, `"values":{"JourneyRef":"journey-1"}`) {
		t.Fatalf("json.Marshal() = %s, want generic values", body)
	}
	if strings.Contains(body, `"userID"`) || strings.Contains(body, `"UserID"`) {
		t.Fatalf("json.Marshal() = %s, must not expose user ID", body)
	}
}
