package nrod

import (
	"encoding/json"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/mongo"
)

type StompClient struct {
	Address   string
	Username  string
	Password  string
	QueueName string

	Queue       *railutils.BatchProcessingQueue
	TiplocCache railutils.TiplocCache
}

func (s *StompClient) Run() {
	s.TiplocCache = railutils.TiplocCache{}
	s.TiplocCache.Setup()

	// Setup batch queue processor first
	s.Queue = &railutils.BatchProcessingQueue{
		Timeout: time.Second * 5,
		Items:   make(chan mongo.WriteModel, 500),
	}
	s.Queue.Process()

	// Start stomp client
	var stompOptions []func(*stomp.Conn) error = []func(*stomp.Conn) error{
		stomp.ConnOpt.Login(s.Username, s.Password),
	}
	conn, err := stomp.Dial("tcp", s.Address, stompOptions...)

	if err != nil {
		log.Fatal().Err(err).Msg("cannot connect to server")
	}

	sub, err := conn.Subscribe(s.QueueName, stomp.AckAuto)
	if err != nil {
		log.Fatal().Str("queue", s.QueueName).Err(err).Msg("cannot subscribe to queue")
	}

	for true {
		msg := <-sub.C

		s.ParseMessages(msg.Body)
	}
}

func (s *StompClient) ParseMessages(messagesBytes []byte) {
	var rawMessages []*json.RawMessage
	json.Unmarshal(messagesBytes, &rawMessages)

	for _, jsonObj := range rawMessages {
		var message *Message
		json.Unmarshal(*jsonObj, &message)

		switch message.Header.MsgType {
		case "0001":
			var activationMessage TrustActivation
			json.Unmarshal(message.Body, &activationMessage)

			activationMessage.Process(s)
		case "0002":
			var cancellationMessage TrustCancellation
			json.Unmarshal(message.Body, &cancellationMessage)

			// pretty.Println(cancellationMessage)
		case "0003":
			var movementMessage TrustMovement
			json.Unmarshal(message.Body, &movementMessage)

			movementMessage.Process(s)
		default:
			log.Debug().Str("type", message.Header.MsgType).Msg("Unhandled message type")
		}
	}
}
