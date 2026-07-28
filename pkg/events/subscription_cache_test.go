package events

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestCompileEventSubscriptionsGroupsByEventType(t *testing.T) {
	subscriptions := []ctdf.UserNotificationSubscription{
		{
			UserID:    "user-1",
			EventType: ctdf.EventTypeServiceAlertCreated,
			Values: ctdf.UserNotificationSubscriptionValues{
				ServiceAlertTypes: []string{string(ctdf.ServiceAlertTypeWarning)},
			},
		},
		{
			UserID:    "user-2",
			EventType: ctdf.EventTypeServiceAlertCreated,
			Values: ctdf.UserNotificationSubscriptionValues{
				ServiceAlertTypes: []string{string(ctdf.ServiceAlertTypeServiceSuspended)},
			},
		},
		{
			UserID:    "user-3",
			EventType: ctdf.EventTypeRealtimeJourneyCreated,
		},
	}

	compiledSubscriptions := compileEventSubscriptions(subscriptions)

	if count := countCompiledEventSubscriptions(compiledSubscriptions); count != 3 {
		t.Fatalf("expected 3 compiled subscriptions, got %d", count)
	}
	if count := len(compiledSubscriptions[ctdf.EventTypeServiceAlertCreated]); count != 2 {
		t.Fatalf("expected 2 service alert subscriptions, got %d", count)
	}
	if count := len(compiledSubscriptions[ctdf.EventTypeRealtimeJourneyCreated]); count != 1 {
		t.Fatalf("expected 1 realtime journey subscription, got %d", count)
	}
}

func TestEventSubscriptionCacheForEventTypeReturnsCopy(t *testing.T) {
	compiledSubscriptions := compileEventSubscriptions([]ctdf.UserNotificationSubscription{
		{
			UserID:    "user-1",
			EventType: ctdf.EventTypeServiceAlertCreated,
			Values: ctdf.UserNotificationSubscriptionValues{
				ServiceAlertTypes: []string{string(ctdf.ServiceAlertTypeWarning)},
			},
		},
	})
	cache := newEventSubscriptionCache()
	cache.byEventType = compiledSubscriptions

	subscriptions := cache.ForEventType(ctdf.EventTypeServiceAlertCreated)
	if len(subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subscriptions))
	}

	subscriptions[0].UserID = "changed"

	subscriptions = cache.ForEventType(ctdf.EventTypeServiceAlertCreated)
	if subscriptions[0].UserID != "user-1" {
		t.Fatalf("expected cached subscription to be unchanged, got %s", subscriptions[0].UserID)
	}
}
