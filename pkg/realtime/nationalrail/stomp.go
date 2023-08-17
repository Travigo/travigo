package nationalrail

import (
	"bytes"
	"compress/gzip"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/rs/zerolog/log"
	"go.mongodb.org/mongo-driver/mongo"
)

type StompClient struct {
	Address   string
	Username  string
	Password  string
	QueueName string
}

func (s *StompClient) Run() {
	// Setup batch queue processor first
	queue := &BatchProcessingQueue{
		Timeout: time.Second * 5,
		Items:   make(chan mongo.WriteModel, 500),
	}
	queue.Process()

	// signals := make(chan os.Signal, 1)
	// signal.Notify(signals, syscall.SIGINT)
	// defer signal.Stop(signals)

	// <-signals // wait for signal
	// go func() {
	// 	<-signals // hard exit on second signal (in case shutdown gets stuck)
	// 	os.Exit(1)
	// }()

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

		b := bytes.NewReader(msg.Body)
		gzipDecoder, err := gzip.NewReader(b)
		if err != nil {
			log.Error().Err(err).Msg("cannot decode gzip stream")
			continue
		}
		defer gzipDecoder.Close()

		pushPortData, err := ParseXMLFile(gzipDecoder)
		if err != nil {
			log.Fatal().Err(err).Msg("Failed to parse push port data xml")
		}

		pushPortData.UpdateRealtimeJourneys(queue)
	}
}
