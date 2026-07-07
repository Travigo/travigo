package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/expr-lang/expr"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/redis_client"

	"github.com/adjust/rmq/v5"
)

type EventsBatchConsumer struct {
	NotifyQueue   rmq.Queue
	Subscriptions *eventSubscriptionCache
}

func NewEventsBatchConsumer() *EventsBatchConsumer {
	notifyQueue, err := redis_client.QueueConnection.OpenQueue("notify-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start notify queue")
	}

	subscriptions := newEventSubscriptionCache()
	if err := subscriptions.Reload(context.Background()); err != nil {
		log.Error().Err(err).Msg("Failed to load event subscriptions")
	}
	subscriptions.StartBackgroundReload(eventSubscriptionRefreshInterval)

	return &EventsBatchConsumer{
		NotifyQueue:   notifyQueue,
		Subscriptions: subscriptions,
	}
}

func (c *EventsBatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	for _, payload := range payloads {
		var event ctdf.Event
		err := json.Unmarshal([]byte(payload), &event)

		if err != nil {
			continue
		}

		log.Info().Str("type", fmt.Sprintf("%s", event.Type)).Msg("Received event")

		var subscriptions []compiledEventSubscription
		if c.Subscriptions != nil {
			subscriptions = c.Subscriptions.ForEventType(event.Type)
		}

		for _, userEventSubscription := range subscriptions {
			output, err := expr.Run(userEventSubscription.Program, event)
			if err != nil {
				continue
			}

			// If expression matches to true then send the notification
			if output == true {
				notificationData := GetNotificationData(&event)

				notification := ctdf.Notification{
					TargetUser: userEventSubscription.UserID,
					Type:       userEventSubscription.NotificationType,
					Title:      notificationData.Title,
					Message:    notificationData.Message,
				}

				notificationBytes, _ := json.Marshal(notification)
				c.NotifyQueue.PublishBytes(notificationBytes)

				log.Info().Str("user", userEventSubscription.UserID).Msg("Sending notification")
			}
		}
	}

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume from queue")
		}
	}
}
