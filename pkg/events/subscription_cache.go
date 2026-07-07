package events

import (
	"context"
	"sync"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
)

const eventSubscriptionRefreshInterval = time.Minute

type compiledEventSubscription struct {
	ctdf.UserEventSubscription
	Program *vm.Program
}

type eventSubscriptionCache struct {
	mu          sync.RWMutex
	byEventType map[ctdf.EventType][]compiledEventSubscription
}

func newEventSubscriptionCache() *eventSubscriptionCache {
	return &eventSubscriptionCache{
		byEventType: map[ctdf.EventType][]compiledEventSubscription{},
	}
}

func (c *eventSubscriptionCache) StartBackgroundReload(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for range ticker.C {
			if err := c.Reload(context.Background()); err != nil {
				log.Error().Err(err).Msg("Failed to reload event subscriptions")
			}
		}
	}()
}

func (c *eventSubscriptionCache) Reload(ctx context.Context) error {
	userEventSubscriptionCollection := database.GetCollection("user_event_subscription")
	cursor, err := userEventSubscriptionCollection.Find(ctx, bson.M{})
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)

	subscriptions := []ctdf.UserEventSubscription{}
	for cursor.Next(ctx) {
		var subscription ctdf.UserEventSubscription
		if err := cursor.Decode(&subscription); err != nil {
			log.Error().Err(err).Msg("Failed to decode UserEventSubscription")
			continue
		}

		subscriptions = append(subscriptions, subscription)
	}
	if err := cursor.Err(); err != nil {
		return err
	}

	compiledSubscriptions := compileEventSubscriptions(subscriptions)

	c.mu.Lock()
	c.byEventType = compiledSubscriptions
	c.mu.Unlock()

	log.Info().
		Int("subscriptions", countCompiledEventSubscriptions(compiledSubscriptions)).
		Int("event_types", len(compiledSubscriptions)).
		Msg("Reloaded event subscriptions")

	return nil
}

func (c *eventSubscriptionCache) ForEventType(eventType ctdf.EventType) []compiledEventSubscription {
	c.mu.RLock()
	defer c.mu.RUnlock()

	subscriptions := c.byEventType[eventType]
	if len(subscriptions) == 0 {
		return nil
	}

	copiedSubscriptions := make([]compiledEventSubscription, len(subscriptions))
	copy(copiedSubscriptions, subscriptions)

	return copiedSubscriptions
}

func compileEventSubscriptions(subscriptions []ctdf.UserEventSubscription) map[ctdf.EventType][]compiledEventSubscription {
	compiledSubscriptions := map[ctdf.EventType][]compiledEventSubscription{}

	for _, subscription := range subscriptions {
		program, err := expr.Compile(subscription.Expression, expr.AsBool(), expr.AllowUndefinedVariables())
		if err != nil {
			log.Error().
				Err(err).
				Str("user", subscription.UserID).
				Str("event_type", string(subscription.EventType)).
				Msg("Failed to compile event subscription expression")
			continue
		}

		compiledSubscriptions[subscription.EventType] = append(compiledSubscriptions[subscription.EventType], compiledEventSubscription{
			UserEventSubscription: subscription,
			Program:               program,
		})
	}

	return compiledSubscriptions
}

func countCompiledEventSubscriptions(subscriptions map[ctdf.EventType][]compiledEventSubscription) int {
	count := 0
	for _, eventSubscriptions := range subscriptions {
		count += len(eventSubscriptions)
	}

	return count
}
