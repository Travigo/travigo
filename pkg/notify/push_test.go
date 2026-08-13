package notify

import "testing"

func TestNotificationWebLink(t *testing.T) {
	if got := notificationWebLink("/journeys/journey-1"); got != "https://travigo.app/journeys/journey-1" {
		t.Fatalf("notification web link = %q", got)
	}

	if got := notificationWebLink("https://example.com/notification"); got != "https://example.com/notification" {
		t.Fatalf("absolute notification web link = %q", got)
	}
}
