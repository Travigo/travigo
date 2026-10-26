package nrod

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
	"go.mongodb.org/mongo-driver/bson"
)

type StompClient struct {
	Address  string
	Username string
	Password string

	// Queue     *railutils.BatchProcessingQueue
	StopCache railutils.StopCache
}

func (s *StompClient) Run() {
	railutils.LoadLateAndCancelledReasons()

	s.StopCache = railutils.StopCache{}
	s.StopCache.Setup()

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

			// Performance Review [Set M] [PR115] [P1] (realtime): Frames run without an in-flight
			// limit under AckAuto. A later movement can beat its activation, find no
			// TrainID mapping and be lost; slow storage also grows concurrent work.
			// Use bounded workers ordered per train run, a bounded retry for missing
			// activations, and acknowledgement after durable success. Apply the same
			// backpressure to VSTP below, ordered by UID/date for create/delete events;
			// return processing errors so overload is retried rather than discarded.
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
	var vstpMessage VSTPMessage
	json.Unmarshal(messagesBytes, &vstpMessage)

	// pretty.Println(vstpMessage.VSTP)

	vstpMessage.Process(s)

	// Performance Review [Set B] [PR116] [P2] (realtime): Every VSTP message also synchronously
	// inserts both its raw payload and parsed representation as diagnostic data.
	// Measure this write/byte volume separately from timetable persistence; sample
	// or batch diagnostics with bounded buffering and explicit retention. A slow
	// diagnostic sink must not keep an unbounded set of processing goroutines alive.
	collection := database.GetCollection("datadump")
	collection.InsertOne(context.Background(), bson.M{
		"type":             "vstp",
		"creationdatetime": time.Now(),
		"document":         string(messagesBytes),
		"parsed":           vstpMessage.VSTP,
	})
}
