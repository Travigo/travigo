package notify

import (
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"

	"github.com/adjust/rmq/v5"
)

type NotifyBatchConsumer struct {
	PushManager *PushManager
}

func NewNotifyBatchConsumer(pushManager *PushManager) *NotifyBatchConsumer {
	return &NotifyBatchConsumer{
		PushManager: pushManager,
	}
}

func (c *NotifyBatchConsumer) Consume(batch rmq.Deliveries) {
	payloads := batch.Payloads()

	for _, payload := range payloads {
		var notification ctdf.Notification
		err := json.Unmarshal([]byte(payload), &notification)

		if err != nil {
			continue
		}

		switch notification.Type {
		case ctdf.NotificationTypePush:
			err = c.PushManager.SendPush(notification)
			if err != nil {
				log.Error().Err(err).Msg("Failed to send Push Notification")
			}
		default:
			log.Error().Str("type", fmt.Sprintf("%s", notification.Type)).Msg("Unknown notification type")
		}
	}

	if ackErrors := batch.Ack(); len(ackErrors) > 0 {
		for _, err := range ackErrors {
			log.Fatal().Err(err).Msg("Failed to consume from queue")
		}
	}
}
