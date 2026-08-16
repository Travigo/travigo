package events

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
)

func TestCompileEventSubscriptionsForDayFiltersSubscriptions(t *testing.T) {
	subscriptions := []ctdf.UserNotificationSubscription{
		{
			UserID:     "monday-user",
			EventType:  ctdf.EventTypeServiceAlertCreated,
			DaysOfWeek: []string{"Monday"},
			Values: ctdf.UserNotificationSubscriptionValues{
				ServiceAlertTypes: []string{string(ctdf.ServiceAlertTypeWarning)},
			},
		},
		{
			UserID:     "tuesday-user",
			EventType:  ctdf.EventTypeServiceAlertCreated,
			DaysOfWeek: []string{"Tuesday"},
			Values: ctdf.UserNotificationSubscriptionValues{
				ServiceAlertTypes: []string{string(ctdf.ServiceAlertTypeWarning)},
			},
		},
		{
			UserID:    "every-day-user",
			EventType: ctdf.EventTypeServiceAlertCreated,
			Values: ctdf.UserNotificationSubscriptionValues{
				ServiceAlertTypes: []string{string(ctdf.ServiceAlertTypeWarning)},
			},
		},
	}

	compiledSubscriptions := compileEventSubscriptionsForDay(subscriptions, time.Monday)
	compiled := compiledSubscriptions[ctdf.EventTypeServiceAlertCreated]

	if len(compiled) != 2 {
		t.Fatalf("compiled Monday subscriptions = %d, want 2", len(compiled))
	}
	for _, subscription := range compiled {
		if subscription.UserID == "tuesday-user" {
			t.Fatal("Tuesday-only subscription was compiled on Monday")
		}
	}
}

func TestDurationUntilNextLocalDay(t *testing.T) {
	location := time.FixedZone("test", 0)
	now := time.Date(2026, time.August, 16, 23, 30, 0, 0, location)

	if got, want := durationUntilNextLocalDay(now), 30*time.Minute; got != want {
		t.Fatalf("durationUntilNextLocalDay() = %s, want %s", got, want)
	}
}

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
			Values: ctdf.UserNotificationSubscriptionValues{
				JourneyRef: "journey-1",
			},
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
