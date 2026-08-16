package routes

import (
	"encoding/json"
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestNotificationSubscriptionLimitForUser(t *testing.T) {
	if got := NotificationSubscriptionLimitForUser("user-1"); got != 10 {
		t.Fatalf("NotificationSubscriptionLimitForUser() = %d, want 10", got)
	}
}

func TestBuildNotificationSubscriptionQuota(t *testing.T) {
	quota := buildNotificationSubscriptionQuota("user-1", 4)

	if quota.Used != 4 || quota.Limit != 10 || quota.Remaining != 6 {
		t.Fatalf("buildNotificationSubscriptionQuota() = %#v, want used=4 limit=10 remaining=6", quota)
	}
}

func TestBuildNotificationSubscriptionQuotaDoesNotReturnNegativeRemaining(t *testing.T) {
	quota := buildNotificationSubscriptionQuota("user-1", 12)

	if quota.Remaining != 0 {
		t.Fatalf("buildNotificationSubscriptionQuota() remaining = %d, want 0", quota.Remaining)
	}
}

func TestValidateNotificationSubscriptionRequest(t *testing.T) {
	validRequest := notificationSubscriptionRequest{
		EventType: ctdf.EventTypeServiceAlertCreated,
		Values: ctdf.UserNotificationSubscriptionValues{
			StopRef:           "stop-1",
			ServiceAlertTypes: []string{"Delays"},
		},
	}

	if got := validateNotificationSubscriptionRequest(validRequest); got != "" {
		t.Fatalf("validateNotificationSubscriptionRequest() = %q, want no error", got)
	}

	validRequest.Values = ctdf.UserNotificationSubscriptionValues{}
	if got := validateNotificationSubscriptionRequest(validRequest); got != "No notification values set" {
		t.Fatalf("validateNotificationSubscriptionRequest() = %q, want missing values error", got)
	}
}

func TestValidateNotificationSubscriptionRequestRejectsUnknownEventType(t *testing.T) {
	request := validJourneyNotificationSubscriptionRequest()
	request.EventType = ctdf.EventType("JourneyEdited")

	if got := validateNotificationSubscriptionRequest(request); got != "Invalid notification event type" {
		t.Fatalf("validateNotificationSubscriptionRequest() = %q, want invalid event type error", got)
	}
}

func TestValidateNotificationSubscriptionRequestRejectsUnknownNotificationDay(t *testing.T) {
	request := validJourneyNotificationSubscriptionRequest()
	request.DaysOfWeek = []string{"Monday", "Funday"}

	if got := validateNotificationSubscriptionRequest(request); got != "Invalid notification day" {
		t.Fatalf("validateNotificationSubscriptionRequest() = %q, want invalid notification day error", got)
	}
}

func TestNotificationSubscriptionRequestDecodesWebUIValues(t *testing.T) {
	var request notificationSubscriptionRequest
	err := json.Unmarshal([]byte(`{
		"eventType": "RealtimeJourneyPlatformChanged",
		"daysOfWeek": ["Monday", "Friday"],
		"values": {
			"JourneyRef": "journey-1",
			"StopRefs": ["stop-1", "stop-2"]
		}
	}`), &request)
	if err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}

	if request.Values.JourneyRef != "journey-1" {
		t.Fatalf("JourneyRef = %q, want journey-1", request.Values.JourneyRef)
	}
	if len(request.DaysOfWeek) != 2 || request.DaysOfWeek[0] != "Monday" || request.DaysOfWeek[1] != "Friday" {
		t.Fatalf("DaysOfWeek = %#v, want [Monday Friday]", request.DaysOfWeek)
	}
	if len(request.Values.StopRefs) != 2 || request.Values.StopRefs[0] != "stop-1" || request.Values.StopRefs[1] != "stop-2" {
		t.Fatalf("StopRefs = %#v, want [stop-1 stop-2]", request.Values.StopRefs)
	}
}

func validJourneyNotificationSubscriptionRequest() notificationSubscriptionRequest {
	return notificationSubscriptionRequest{
		EventType: ctdf.EventTypeRealtimeJourneyCancelled,
		Values: ctdf.UserNotificationSubscriptionValues{
			JourneyRef: "journey-1",
		},
	}
}
