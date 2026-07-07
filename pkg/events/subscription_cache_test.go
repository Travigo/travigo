package events

import (
	"testing"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestCompileEventSubscriptionsGroupsByEventType(t *testing.T) {
	subscriptions := []ctdf.UserEventSubscription{
		{
			UserID:     "user-1",
			EventType:  ctdf.EventTypeRealtimeJourneyCreated,
			Expression: "true",
		},
		{
			UserID:     "user-2",
			EventType:  ctdf.EventTypeRealtimeJourneyCreated,
			Expression: "true",
		},
		{
			UserID:     "user-3",
			EventType:  ctdf.EventTypeRealtimeJourneyPlatformChanged,
			Expression: "true",
		},
		{
			UserID:     "user-4",
			EventType:  ctdf.EventTypeServiceAlertCreated,
			Expression: "(",
		},
	}

	compiledSubscriptions := compileEventSubscriptions(subscriptions)

	if count := countCompiledEventSubscriptions(compiledSubscriptions); count != 3 {
		t.Fatalf("expected 3 compiled subscriptions, got %d", count)
	}
	if count := len(compiledSubscriptions[ctdf.EventTypeRealtimeJourneyCreated]); count != 2 {
		t.Fatalf("expected 2 created subscriptions, got %d", count)
	}
	if count := len(compiledSubscriptions[ctdf.EventTypeRealtimeJourneyPlatformChanged]); count != 1 {
		t.Fatalf("expected 1 platform changed subscription, got %d", count)
	}
	if count := len(compiledSubscriptions[ctdf.EventTypeServiceAlertCreated]); count != 0 {
		t.Fatalf("expected invalid service alert subscription to be skipped, got %d", count)
	}
}

func TestEventSubscriptionCacheForEventTypeReturnsCopy(t *testing.T) {
	compiledSubscriptions := compileEventSubscriptions([]ctdf.UserEventSubscription{
		{
			UserID:     "user-1",
			EventType:  ctdf.EventTypeRealtimeJourneyCreated,
			Expression: "true",
		},
	})
	cache := newEventSubscriptionCache()
	cache.byEventType = compiledSubscriptions

	subscriptions := cache.ForEventType(ctdf.EventTypeRealtimeJourneyCreated)
	if len(subscriptions) != 1 {
		t.Fatalf("expected 1 subscription, got %d", len(subscriptions))
	}

	subscriptions[0].UserID = "changed"

	subscriptions = cache.ForEventType(ctdf.EventTypeRealtimeJourneyCreated)
	if subscriptions[0].UserID != "user-1" {
		t.Fatalf("expected cached subscription to be unchanged, got %s", subscriptions[0].UserID)
	}
}
