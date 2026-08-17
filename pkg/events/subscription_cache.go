package events

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
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
	commuteSubscriptions, err := compileCommuteSubscriptions(ctx, time.Now())
	if err != nil {
		return err
	}
	for eventType, subscriptions := range commuteSubscriptions {
		compiledSubscriptions[eventType] = append(compiledSubscriptions[eventType], subscriptions...)
	}

	c.mu.Lock()
	c.byEventType = compiledSubscriptions
	c.mu.Unlock()

	log.Info().
		Int("subscriptions", countCompiledEventSubscriptions(compiledSubscriptions)).
		Int("event_types", len(compiledSubscriptions)).
		Msg("Reloaded event subscriptions")

	return nil
}

func compileCommuteSubscriptions(ctx context.Context, now time.Time) (map[ctdf.EventType][]ctdf.UserNotificationSubscription, error) {
	cursor, err := database.GetCollection("user_commutes").Find(ctx, bson.M{})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	commutes := []ctdf.UserCommute{}
	if err := cursor.All(ctx, &commutes); err != nil {
		return nil, err
	}
	compiled := map[ctdf.EventType][]ctdf.UserNotificationSubscription{}
	for _, commute := range commutes {
		for _, subscription := range subscriptionsForCommute(ctx, commute, now) {
			if err := subscription.Compile(); err != nil {
				log.Error().Err(err).Str("commute", commute.PrimaryIdentifier).Str("event_type", string(subscription.EventType)).Msg("Failed to compile commute notification")
				continue
			}
			compiled[subscription.EventType] = append(compiled[subscription.EventType], subscription)
		}
	}
	return compiled, nil
}

func commuteMatchesDay(commute ctdf.UserCommute, now time.Time, location *time.Location) bool {
	if location != nil {
		now = now.In(location)
	}
	for _, day := range commute.DaysOfWeek {
		if day == now.Weekday().String() {
			return true
		}
	}
	return false
}

func subscriptionsForCommute(ctx context.Context, commute ctdf.UserCommute, now time.Time) []ctdf.UserNotificationSubscription {
	origin, originErr := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: commute.OriginRef})
	destination, destinationErr := dataaggregator.Lookup[*ctdf.Stop](query.Stop{Identifier: commute.DestinationRef})
	if originErr != nil || destinationErr != nil || origin == nil || destination == nil {
		log.Warn().Err(firstError(originErr, destinationErr)).Str("commute", commute.PrimaryIdentifier).Msg("Skipping commute with unavailable stop")
		return nil
	}
	location := time.Local
	if destination.Timezone != "" {
		if loaded, err := time.LoadLocation(destination.Timezone); err == nil {
			location = loaded
		}
	}
	if !commuteMatchesDay(commute, now, location) {
		return nil
	}
	arrivalBy, arrivalErr := commuteTimeOnDate(now, location, commute.ArrivalAtDestinationTime)
	returnDeparture, returnErr := commuteTimeOnDate(now, location, commute.ReturnDepartureTime)
	if arrivalErr != nil || returnErr != nil {
		log.Warn().Err(firstError(arrivalErr, returnErr)).Str("commute", commute.PrimaryIdentifier).Msg("Skipping commute with invalid clock time")
		return nil
	}
	queries := []query.JourneyPlan{
		{Context: ctx, OriginStop: origin, DestinationStop: destination, Count: 1, StartDateTime: arrivalBy.Add(-12 * time.Hour), ArrivalByDateTime: arrivalBy, MaxChanges: 3, MaxJourneyDuration: 12 * time.Hour, MaxTransferDistanceMetres: 1000, MaxExpandedLabels: 200000, MaxSearchDuration: 10 * time.Second},
		{Context: ctx, OriginStop: destination, DestinationStop: origin, Count: 1, StartDateTime: returnDeparture, MaxChanges: 3, MaxJourneyDuration: 12 * time.Hour, MaxTransferDistanceMetres: 1000, MaxExpandedLabels: 200000, MaxSearchDuration: 10 * time.Second},
	}
	plans := make([]ctdf.JourneyPlan, 0, 2)
	for _, commuteQuery := range queries {
		result, err := dataaggregator.Lookup[*ctdf.JourneyPlanResults](commuteQuery)
		if err != nil {
			log.Warn().Err(err).Str("commute", commute.PrimaryIdentifier).Msg("Failed to resolve commute route")
			continue
		}
		if result != nil && len(result.JourneyPlans) > 0 {
			plans = append(plans, result.JourneyPlans[0])
		}
	}
	return subscriptionsForCommutePlans(commute, plans)
}

func commuteTimeOnDate(now time.Time, location *time.Location, value string) (time.Time, error) {
	clock, err := time.Parse("15:04", value)
	if err != nil {
		return time.Time{}, err
	}
	localNow := now.In(location)
	return time.Date(localNow.Year(), localNow.Month(), localNow.Day(), clock.Hour(), clock.Minute(), 0, 0, location), nil
}

func subscriptionsForCommutePlans(commute ctdf.UserCommute, plans []ctdf.JourneyPlan) []ctdf.UserNotificationSubscription {
	values := ctdf.UserNotificationSubscriptionValues{ServiceAlertTypes: ctdf.AllServiceAlertTypes()}
	for _, plan := range plans {
		for _, item := range plan.RouteItems {
			if item.Type != ctdf.JourneyPlanRouteItemTypeJourney || item.Journey == nil {
				continue
			}
			values.JourneyRefs = append(values.JourneyRefs, item.Journey.PrimaryIdentifier)
			values.ServiceRefs = append(values.ServiceRefs, item.Journey.ServiceRef)
			values.PlatformStopRefs = append(values.PlatformStopRefs, item.OriginStopRef)
			values.StopRefs = append(values.StopRefs, item.OriginStopRef, item.DestinationStopRef)
			for _, path := range item.Journey.Path {
				if path != nil {
					values.StopRefs = append(values.StopRefs, path.OriginStopRef, path.DestinationStopRef)
				}
			}
		}
	}
	values.StopRefs = uniqueStrings(values.StopRefs)
	values.ServiceRefs = uniqueStrings(values.ServiceRefs)
	values.JourneyRefs = uniqueStrings(values.JourneyRefs)
	values.PlatformStopRefs = uniqueStrings(values.PlatformStopRefs)
	if len(values.JourneyRefs) == 0 && len(values.StopRefs) == 0 && len(values.ServiceRefs) == 0 {
		return nil
	}
	newSubscription := func(eventType ctdf.EventType) ctdf.UserNotificationSubscription {
		return ctdf.UserNotificationSubscription{PrimaryIdentifier: commute.PrimaryIdentifier, UserID: commute.UserID, EventType: eventType, Values: values}
	}
	return []ctdf.UserNotificationSubscription{
		newSubscription(ctdf.EventTypeServiceAlertCreated),
		newSubscription(ctdf.EventTypeRealtimeJourneyCancelled),
		newSubscription(ctdf.EventTypeRealtimeJourneyOverlayCreated),
		newSubscription(ctdf.EventTypeRealtimeJourneyDelayed),
		newSubscription(ctdf.EventTypeRealtimeJourneyPlatformSet),
		newSubscription(ctdf.EventTypeRealtimeJourneyPlatformChanged),
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func firstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
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
