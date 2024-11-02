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

	// testMessage := "{\"VSTPCIFMsgV1\":{\"schedule\":{\"schedule_segment\":[{\"schedule_location\":[{\"location\":{\"tiploc\":{\"tiploc_id\":\"WATRLOO\"}},\"scheduled_pass_time\":\" \",\"scheduled_departure_time\":\"163000\",\"scheduled_arrival_time\":\" \",\"public_departure_time\":\"163000\",\"public_arrival_time\":\" \",\"CIF_platform\":\"22\",\"CIF_path\":\" \",\"CIF_line\":\"WR1\",\"CIF_activity\":\"TB\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"WATRLWC\"}},\"scheduled_pass_time\":\"163130\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_line\":\"WFL\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"VAUXHAL\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"163430\",\"scheduled_arrival_time\":\"163330\",\"public_departure_time\":\"163400\",\"public_arrival_time\":\"163400\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"NINELMJ\"}},\"scheduled_pass_time\":\"163600\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \",\"CIF_line\":\"WL\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"QTRDBAT\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"163730\",\"scheduled_arrival_time\":\"163700\",\"public_departure_time\":\"163700\",\"public_arrival_time\":\"163700\",\"CIF_platform\":\"3\",\"CIF_line\":\"WFL\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"CLPHMJN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"164030\",\"scheduled_arrival_time\":\"163930\",\"public_departure_time\":\"164000\",\"public_arrival_time\":\"164000\",\"CIF_platform\":\"5\",\"CIF_line\":\"FL\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"WDWTOWN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"164300\",\"scheduled_arrival_time\":\"164230\",\"public_departure_time\":\"164300\",\"public_arrival_time\":\"164300\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"PUTNEY\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"164600\",\"scheduled_arrival_time\":\"164500\",\"public_departure_time\":\"164600\",\"public_arrival_time\":\"164500\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"BARNES\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"165130\",\"scheduled_arrival_time\":\"164830\",\"public_departure_time\":\"165100\",\"public_arrival_time\":\"164900\",\"CIF_platform\":\"3\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"MRTLKE\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"165400\",\"scheduled_arrival_time\":\"165330\",\"public_departure_time\":\"165400\",\"public_arrival_time\":\"165400\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"NSHEEN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"165600\",\"scheduled_arrival_time\":\"165530\",\"public_departure_time\":\"165600\",\"public_arrival_time\":\"165600\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"RICHMND\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"165830\",\"scheduled_arrival_time\":\"165730\",\"public_departure_time\":\"165800\",\"public_arrival_time\":\"165800\",\"CIF_platform\":\"1\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"STMGTS\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"170100\",\"scheduled_arrival_time\":\"170030\",\"public_departure_time\":\"170100\",\"public_arrival_time\":\"170100\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"TWCKNHM\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"170330\",\"scheduled_arrival_time\":\"170230\",\"public_departure_time\":\"170300\",\"public_arrival_time\":\"170300\",\"CIF_platform\":\"5\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"STRWBYH\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"170730\",\"scheduled_arrival_time\":\"170630\",\"public_departure_time\":\"170700\",\"public_arrival_time\":\"170700\",\"CIF_platform\":\"1\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"SHCKLGJ\"}},\"scheduled_pass_time\":\"170830\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"TEDNGTN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"171130\",\"scheduled_arrival_time\":\"171000\",\"public_departure_time\":\"171100\",\"public_arrival_time\":\"171000\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"HAMWICK\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"171400\",\"scheduled_arrival_time\":\"171330\",\"public_departure_time\":\"171400\",\"public_arrival_time\":\"171400\",\"CIF_platform\":\"1\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"KGSTON\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"171830\",\"scheduled_arrival_time\":\"171530\",\"public_departure_time\":\"171800\",\"public_arrival_time\":\"171600\",\"CIF_platform\":\"3\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"NRBITON\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"172030\",\"scheduled_arrival_time\":\"172000\",\"public_departure_time\":\"172000\",\"public_arrival_time\":\"172000\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"NEWMLDN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"172500\",\"scheduled_arrival_time\":\"172400\",\"public_departure_time\":\"172500\",\"public_arrival_time\":\"172400\",\"CIF_line\":\"SL\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"RAYNSPK\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"172800\",\"scheduled_arrival_time\":\"172700\",\"public_departure_time\":\"172800\",\"public_arrival_time\":\"172700\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"WIMBLDN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"173200\",\"scheduled_arrival_time\":\"173100\",\"public_departure_time\":\"173200\",\"public_arrival_time\":\"173100\",\"CIF_platform\":\"5\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"ERLFLD\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"173530\",\"scheduled_arrival_time\":\"173500\",\"public_departure_time\":\"173500\",\"public_arrival_time\":\"173500\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"CLPHMJN\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"173930\",\"scheduled_arrival_time\":\"173830\",\"public_departure_time\":\"173900\",\"public_arrival_time\":\"173900\",\"CIF_platform\":\"10\",\"CIF_line\":\"MSL\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"VAUXHAL\"}},\"scheduled_pass_time\":\"      \",\"scheduled_departure_time\":\"174430\",\"scheduled_arrival_time\":\"174330\",\"public_departure_time\":\"174400\",\"public_arrival_time\":\"174400\",\"CIF_activity\":\"T\"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"WATRLWC\"}},\"scheduled_pass_time\":\"174630\",\"scheduled_departure_time\":\"      \",\"scheduled_arrival_time\":\"      \",\"public_departure_time\":\"      \",\"public_arrival_time\":\"      \"},{\"location\":{\"tiploc\":{\"tiploc_id\":\"WATRLOO\"}},\"scheduled_pass_time\":\" \",\"scheduled_departure_time\":\" \",\"scheduled_arrival_time\":\"174900\",\"public_departure_time\":\" \",\"public_arrival_time\":\"174900\",\"CIF_platform\":\"5\",\"CIF_performance_allowance\":\" \",\"CIF_pathing_allowance\":\" \",\"CIF_line\":\" \",\"CIF_engineering_allowance\":\" \",\"CIF_activity\":\"TF\"}],\"signalling_id\":\"2O47\",\"atoc_code\":\"SW\",\"CIF_train_service_code\":\"24671505\",\"CIF_train_class\":\"S\",\"CIF_train_category\":\"OO\",\"CIF_speed\":\"075\",\"CIF_power_type\":\"EMU\",\"CIF_headcode\":\"32\",\"CIF_course_indicator\":\"1\"}],\"transaction_type\":\"Create\",\"train_status\":\"1\",\"schedule_start_date\":\"2024-11-02\",\"schedule_end_date\":\"2024-11-02\",\"schedule_days_runs\":\"0000010\",\"applicable_timetable\":\"Y\",\"CIF_train_uid\":\"L61329\",\"CIF_stp_indicator\":\"C\"},\"Sender\":{\"organisation\":\"Network Rail\",\"application\":\"TSIA\",\"component\":\"TSIA\",\"userID\":\"#QCP0003\",\"sessionID\":\"OJ06000\"},\"classification\":\"industry\",\"timestamp\":\"1730507006000\",\"owner\":\"Network Rail\",\"originMsgId\":\"2024-11-02T00:23:26-00:00@vstp.networkrail.co.uk\"}}"
	// // testMessage := "{\"VSTPCIFMsgV1\":{\"schedule\":{\"transaction_type\":\"Delete\",\"train_status\":\" \",\"schedule_start_date\":\"2024-11-02\",\"schedule_end_date\":\"2024-11-02\",\"schedule_days_runs\":\"0000010\",\"CIF_train_uid\":\"C76967\",\"CIF_stp_indicator\":\"O\",\"CIF_bank_holiday_running\":\" \"},\"Sender\":{\"organisation\":\"Network Rail\",\"application\":\"TSIA\",\"component\":\"TSIA\",\"userID\":\"#QRI3727\",\"sessionID\":\"OY04000\"},\"classification\":\"industry\",\"timestamp\":\"1730500383000\",\"owner\":\"Network Rail\",\"originMsgId\":\"2024-11-01T22:33:03-00:00@vstp.networkrail.co.uk\"}}"
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
