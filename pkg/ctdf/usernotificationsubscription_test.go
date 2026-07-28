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
		EventType:         EventTypeServiceAlertCreated,
		Values: UserNotificationSubscriptionValues{
			StopRef:           "stop-1",
			ServiceAlertTypes: []string{"Delays"},
		},
	}

	data, err := json.Marshal(subscription)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	body := string(data)
	if !strings.Contains(body, `"eventType":"ServiceAlertCreated"`) {
		t.Fatalf("json.Marshal() = %s, want singular eventType", body)
	}
	if strings.Contains(body, `"events"`) {
		t.Fatalf("json.Marshal() = %s, must not contain events array", body)
	}
	if !strings.Contains(body, `"values":{"serviceAlertTypes":["Delays"],"stopRef":"stop-1","serviceRef":""}`) {
		t.Fatalf("json.Marshal() = %s, want typed values", body)
	}
	if strings.Contains(body, `"userID"`) || strings.Contains(body, `"UserID"`) {
		t.Fatalf("json.Marshal() = %s, must not expose user ID", body)
	}
}
