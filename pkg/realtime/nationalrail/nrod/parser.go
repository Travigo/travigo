package nrod

import (
	"encoding/json"

	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
)

type Message struct {
	Header Header          `json:"header"`
	Body   json.RawMessage `json:"body"`
}

type Header struct {
	MsgType            string `json:"msg_type"`
	SourceDevID        string `json:"source_dev_id"`
	UserID             string `json:"user_id"`
	OriginalDataSource string `json:"original_data_source"`
	MsgQueueTimestamp  string `json:"msg_queue_timestamp"`
	SourceSystemID     string `json:"source_system_id"`
}

func ParseMessages(messagesBytes []byte) {
	var rawMessages []*json.RawMessage
	json.Unmarshal(messagesBytes, &rawMessages)

	for _, jsonObj := range rawMessages {
		var message *Message
		json.Unmarshal(*jsonObj, &message)

		switch message.Header.MsgType {
		case "0001":
			var activationMessage TrustActivation
			json.Unmarshal(message.Body, &activationMessage)

			log.Info().
				Str("trainid", activationMessage.TrainID).
				Str("trainuid", activationMessage.TrainUID).
				Str("referencedate", activationMessage.TrainPlannedOriginTimestamp).
				Msg("Train activated")
		case "0002":
			var cancellationMessage TrustCancellation
			json.Unmarshal(message.Body, &cancellationMessage)

			pretty.Println(cancellationMessage)
		case "0003":
			var movementMessage TrustMovement
			json.Unmarshal(message.Body, &movementMessage)

			pretty.Println(movementMessage)
		default:
			log.Debug().Str("type", message.Header.MsgType).Msg("Unhandled message type")
		}
	}
}
