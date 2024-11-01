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

	// testMessage := "{\"VSTPCIFMsgV1\":{\"schedule\":{\"schedule_segment\":[{\"schedule_location\":[{\"location\":{\"tiploc\":{\"tiploc_id\":\"ROMFSDG\"}},\"scheduled_pass_time\":\" \",\"scheduled_departure_time\":\"100300\",\"scheduled_arrival_time\":\" \",\"public_departure_time\":\"      \",\"public_arrival_time\":\" \",\"CIF_path\":\" \",\"CIF_activity\":\"TB\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"ROMFORD\"}},\"scheduled_pass_time\":\"100630\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"5\",\"CIF_line\":\"EL\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"GIDEAPK\"}},\"scheduled_pass_time\":\"100900\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"4\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"GIDEPKM\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"103130\",\"scheduled_arrival_time\":\"101100\",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"GIDEAPK\"}},\"scheduled_pass_time\":\"103330\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"3\",\"CIF_line\":\"EL\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"ROMFORD\"}},\"scheduled_pass_time\":\"103600\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"4\",\"CIF_line\":\"ML\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"ILFORD\"}},\"scheduled_pass_time\":\"104300\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"1\",\"CIF_line\":\"ML\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"FRSTGTJ\"}},\"scheduled_pass_time\":\"104500\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_line\":\"ML\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"STFD\"}},\"scheduled_pass_time\":\"105000\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"10\",\"CIF_pathing_allowance\":\"2\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"CHNELSJ\"}},\"scheduled_pass_time\":\"105530\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"HMEADSJ\"}},\"scheduled_pass_time\":\"105630\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"TMPLMEJ\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"111330\",\"scheduled_arrival_time\":\"105830\",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"COPR105\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"112530\",\"scheduled_arrival_time\":\"111830\",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"COPRMLJ\"}},\"scheduled_pass_time\":\"112800\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"TTNHMSJ\"}},\"scheduled_pass_time\":\"112900\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_pathing_allowance\":\"2\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"BRIMSDN\"}},\"scheduled_pass_time\":\"113700\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_pathing_allowance\":\"2\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"CHESHNT\"}},\"scheduled_pass_time\":\"114300\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"2\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"BROXBRN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"120800\",\"scheduled_arrival_time\":\"114800\",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"4\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"BROXBNJ\"}},\"scheduled_pass_time\":\"121300\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"HRLWTWN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"124800\",\"scheduled_arrival_time\":\"121900\",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"4\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"BSHPSFD\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"130200\",\"scheduled_arrival_time\":\"125900\",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_platform\":\"1\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"BSHPSFY\"}},\"scheduled_pass_time\":\" \",\"scheduled_departure_time\":\" \",\"scheduled_arrival_time\":\"130700\",\"public_departure_time\":\" \",\"public_arrival_time\":\"      \",\"CIF_performance_allowance\":\" \",\"CIF_pathing_allowance\":\" \",\"CIF_line\":\" \",\"CIF_engineering_allowance\":\" \",\"CIF_activity\":\"TF\"}],\"signalling_id\":\"6J24\",\"CIF_train_service_code\":\"53693903\",\"CIF_train_category\":\"DH\",\"CIF_speed\":\"060\",\"CIF_power_type\":\"D\",\"CIF_course_indicator\":\"1\"}],\"transaction_type\":\"Create\",\"train_status\":\" \",\"schedule_start_date\":\"2024-11-01\",\"schedule_end_date\":\"2024-11-01\",\"schedule_days_runs\":\"0000100\",\"applicable_timetable\":\"N\",\"CIF_train_uid\":\" 22674\",\"CIF_stp_indicator\":\"N\"},\"Sender\":{\"organisation\":\"Network Rail\",\"application\":\"TSIA\",\"component\":\"INTEGRALE-VSTP\",\"userID\":\"#QHPC002\",\"sessionID\":\"CS10000\"},\"classification\":\"industry\",\"timestamp\":\"1730410232000\",\"owner\":\"Network Rail\",\"originMsgId\":\"2024-10-31T21:30:32-00:00@vstp.networkrail.co.uk\"}}"
	// testMessage := "{\"VSTPCIFMsgV1\":{\"schedule\":{\"transaction_type\":\"Delete\",\"train_status\":\" \",\"schedule_start_date\":\"2024-11-02\",\"schedule_end_date\":\"2024-11-02\",\"schedule_days_runs\":\"0000010\",\"CIF_train_uid\":\"C76967\",\"CIF_stp_indicator\":\"O\",\"CIF_bank_holiday_running\":\" \"},\"Sender\":{\"organisation\":\"Network Rail\",\"application\":\"TSIA\",\"component\":\"TSIA\",\"userID\":\"#QRI3727\",\"sessionID\":\"OY04000\"},\"classification\":\"industry\",\"timestamp\":\"1730500383000\",\"owner\":\"Network Rail\",\"originMsgId\":\"2024-11-01T22:33:03-00:00@vstp.networkrail.co.uk\"}}"
	// s.ParseVSTPMessages([]byte(testMessage))
	// return

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
	var vstpMessage VSTPMessage
	json.Unmarshal(messagesBytes, &vstpMessage)

	// pretty.Println(vstpMessage.VSTP)

	vstpMessage.Process(s)

	collection := database.GetCollection("datadump")
	collection.InsertOne(context.Background(), bson.M{
		"type":             "vstp",
		"creationdatetime": time.Now(),
		"document":         string(messagesBytes),
		"parsed":           vstpMessage.VSTP,
	})
}
