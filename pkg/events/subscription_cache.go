package events

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const eventSubscriptionRefreshInterval = 5 * time.Minute

type notificationSubscriptionCache struct {
	mu          sync.RWMutex
	byEventType map[ctdf.EventType][]ctdf.UserNotificationSubscription
}

func newEventSubscriptionCache() *notificationSubscriptionCache {
	return &notificationSubscriptionCache{
		byEventType: map[ctdf.EventType][]ctdf.UserNotificationSubscription{},
	}
}

func (c *notificationSubscriptionCache) StartBackgroundReload(interval time.Duration) {
	go func() {
		for {
			wait := interval
			untilNextDay := durationUntilNextLocalDay(time.Now())
			if untilNextDay < wait {
				wait = untilNextDay
			}

			timer := time.NewTimer(wait)
			<-timer.C
			if err := c.Reload(context.Background()); err != nil {
				log.Error().Err(err).Msg("Failed to reload event subscriptions")
			}
		}
	}()
}

func durationUntilNextLocalDay(now time.Time) time.Duration {
	nextDay := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	return nextDay.Sub(now)
}

func (c *notificationSubscriptionCache) Reload(ctx context.Context) error {
	userNotificationSubscriptionCollection := database.GetCollection("user_notification_subscriptions")
	cursor, err := userNotificationSubscriptionCollection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	subscriptions := []ctdf.UserNotificationSubscription{}
	for cursor.Next(ctx) {
		var subscription ctdf.UserNotificationSubscription
		if err := cursor.Decode(&subscription); err != nil {
			log.Error().Err(err).Msg("Failed to decode UserNotificationSubscription")
			continue
		}

		subscriptions = append(subscriptions, subscription)
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	compiledSubscriptions := compileEventSubscriptionsForDay(subscriptions, time.Now().Weekday())

	c.mu.Lock()
	c.byEventType = compiledSubscriptions
	c.mu.Unlock()

	log.Info().
		Int("subscriptions", countCompiledEventSubscriptions(compiledSubscriptions)).
		Int("event_types", len(compiledSubscriptions)).
		Msg("Reloaded event subscriptions")

	return nil
}

func (c *notificationSubscriptionCache) ForEventType(eventType ctdf.EventType) []ctdf.UserNotificationSubscription {
	c.mu.RLock()
	defer c.mu.RUnlock()

	subscriptions := c.byEventType[eventType]
	if len(subscriptions) == 0 {
		return nil
	}

	copiedSubscriptions := make([]ctdf.UserNotificationSubscription, len(subscriptions))
	copy(copiedSubscriptions, subscriptions)

	return copiedSubscriptions
}

func compileEventSubscriptions(subscriptions []ctdf.UserNotificationSubscription) map[ctdf.EventType][]ctdf.UserNotificationSubscription {
	return compileEventSubscriptionsForDay(subscriptions, time.Now().Weekday())
}

func compileEventSubscriptionsForDay(subscriptions []ctdf.UserNotificationSubscription, day time.Weekday) map[ctdf.EventType][]ctdf.UserNotificationSubscription {
	compiledSubscriptions := map[ctdf.EventType][]ctdf.UserNotificationSubscription{}

	for _, subscription := range subscriptions {
		if len(subscription.DaysOfWeek) > 0 && !subscriptionMatchesDay(subscription, day) {
			continue
		}

		err := subscription.Compile()
		if err != nil {
			log.Error().
				Err(err).
				Str("user", subscription.UserID).
				Str("event_type", string(subscription.EventType)).
				Msg("Failed to compile event subscription expression")
			continue
		}

		compiledSubscriptions[subscription.EventType] = append(compiledSubscriptions[subscription.EventType], subscription)
	}

	return compiledSubscriptions
}

func subscriptionMatchesDay(subscription ctdf.UserNotificationSubscription, day time.Weekday) bool {
	for _, configuredDay := range subscription.DaysOfWeek {
		if configuredDay == day.String() {
			return true
		}
	}

	return false
}

func countCompiledEventSubscriptions(subscriptions map[ctdf.EventType][]ctdf.UserNotificationSubscription) int {
	count := 0
	for _, eventSubscriptions := range subscriptions {
		count += len(eventSubscriptions)
	}

	return count
}
