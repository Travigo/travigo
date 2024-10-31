package nrod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/kr/pretty"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type StompClient struct {
	Address  string
	Username string
	Password string

	Queue     *railutils.BatchProcessingQueue
	StopCache railutils.StopCache
}

func (s *StompClient) Run() {
	railutils.LoadLateAndCancelledReasons()

	s.StopCache = railutils.StopCache{}
	s.StopCache.Setup()

	// Setup batch queue processor first
	s.Queue = &railutils.BatchProcessingQueue{
		Timeout: time.Second * 5,
		Items:   make(chan mongo.WriteModel, 500),
	}
	s.Queue.Process()

	// Start stomp client
	var stompOptions []func(*stomp.Conn) error = []func(*stomp.Conn) error{
		stomp.ConnOpt.Login(s.Username, s.Password),
		stomp.ConnOpt.HeartBeat(5*time.Second, 5*time.Second),
	}
	conn, err := stomp.Dial("tcp", s.Address, stompOptions...)

	if err != nil {
		log.Fatal().Err(err).Msg("cannot connect to server")
	}

	go s.StartTrainMovementSubscription(conn)
	go s.StartVSTPSubscription(conn)
}

func (s *StompClient) StartTrainMovementSubscription(conn *stomp.Conn) {
	queueName := "/topic/TRAIN_MVT_ALL_TOC"

	sub, err := conn.Subscribe(queueName, stomp.AckAuto)
	if err != nil {
		log.Fatal().Str("queue", queueName).Err(err).Msg("cannot subscribe to queue")
	}

	log.Info().Str("queue", queueName).Msg("Started subscription")

	for true {
		if !sub.Active() {
			log.Fatal().Str("queue", queueName).Msg("STOMP channel no longer active")
		}
		msg := <-sub.C

		if msg != nil {
			if msg.Err != nil {
				log.Fatal().Str("queue", queueName).Err(err).Msg("STOMP error")
			}

			go s.ParseTrainMovementMessages(msg.Body)
		}
	}
}

func (s *StompClient) ParseTrainMovementMessages(messagesBytes []byte) {
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

			cancellationMessage.Process(s)
		case "0003":
			var movementMessage TrustMovement
			json.Unmarshal(message.Body, &movementMessage)

			movementMessage.Process(s)
		case "0005":
			var reinstatementMessage TrustReinstatement
			json.Unmarshal(message.Body, &reinstatementMessage)

			reinstatementMessage.Process(s)
		default:
			log.Error().Str("type", message.Header.MsgType).Msg("Unhandled message type")
		}
	}
}

func (s *StompClient) StartVSTPSubscription(conn *stomp.Conn) {
	queueName := "/topic/VSTP_ALL"

	sub, err := conn.Subscribe(queueName, stomp.AckAuto)
	if err != nil {
		log.Fatal().Str("queue", queueName).Err(err).Msg("cannot subscribe to queue")
	}

	log.Info().Str("queue", queueName).Msg("Started subscription")

	for true {
		if !sub.Active() {
			log.Fatal().Str("queue", queueName).Msg("STOMP channel no longer active")
		}
		msg := <-sub.C

		if msg != nil {
			if msg.Err != nil {
				log.Fatal().Str("queue", queueName).Err(msg.Err).Msg("STOMP error")
			}

			go s.ParseVSTPMessages(msg.Body)
		}
	}
}

func (s *StompClient) ParseVSTPMessages(messagesBytes []byte) {
	collection := database.GetCollection("datadump")
	collection.InsertOne(context.Background(), bson.M{"type": "vstp", "document": string(messagesBytes)})

	pretty.Println(string(messagesBytes))
}
