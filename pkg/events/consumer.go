package events

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/antonmedv/expr"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/adjust/rmq/v5"
)

type EventsBatchConsumer struct {
	NotifyQueue rmq.Queue
}

func NewEventsBatchConsumer() *EventsBatchConsumer {
	notifyQueue, err := redis_client.QueueConnection.OpenQueue("notify-queue")
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to start notify queue")
	}

	return &EventsBatchConsumer{
		NotifyQueue: notifyQueue,
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

		userEventSubscriptionCollection := database.GetCollection("user_event_subscription")
		cursor, _ := userEventSubscriptionCollection.Find(context.Background(), bson.M{
			"eventtype": event.Type,
		})

		for cursor.Next(context.TODO()) {
			var userEventSubscription ctdf.UserEventSubscription
			err := cursor.Decode(&userEventSubscription)
			if err != nil {
				log.Error().Err(err).Msg("Failed to decode UserEventSubscription")
				continue
			}

			program, err := expr.Compile(userEventSubscription.Expression, expr.AsBool(), expr.AllowUndefinedVariables())
			if err != nil {
				continue
			}

			output, err := expr.Run(program, event.Body)
			if err != nil {
				continue
			}

			// If expression matches to true then send the notification
			if output == true {
				notificationData := event.GetNotificationData()

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
