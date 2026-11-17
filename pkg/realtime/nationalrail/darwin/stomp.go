package darwin

import (
	"bytes"
	"compress/gzip"
	"time"

	"github.com/go-stomp/stomp/v3"
	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/realtime/nationalrail/railutils"
)

type StompClient struct {
	Address    string
	Username   string
	Password   string
	QueueName  string
	Workers    int
	QueueDepth int
}

var stopCache railutils.StopCache

func (s *StompClient) Run() {
	railutils.LoadLateAndCancelledReasons()

	stopCache = railutils.StopCache{}
	stopCache.Setup()

	dispatcher := newDarwinDispatcher(s.Workers, s.QueueDepth)
	defer dispatcher.Close()
	dispatcher.LogStats(30 * time.Second)

	go RetryRecords(dispatcher)

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
		if !sub.Active() {
			log.Fatal().Msg("STOMP channel no longer active")
		}
		msg := <-sub.C

		if msg != nil {
			if msg.Err != nil {
				log.Fatal().Err(err).Msg("STOMP error")
			}

			b := bytes.NewReader(msg.Body)
			gzipDecoder, err := gzip.NewReader(b)
			if err != nil {
				log.Fatal().Err(err).Msg("cannot decode gzip stream")
				continue
			}

			pushPortData, err := ParseXMLFile(gzipDecoder)
			gzipDecoder.Close()
			if err != nil {
				log.Fatal().Err(err).Msg("Failed to parse push port data xml")
			}

			// Independent train runs are processed concurrently, while updates
			// for the same run stay in feed order on one dispatcher shard.
			// Performance Review [Set M] [PR105] [P1]: AckAuto still follows
			// delivery rather than durable processing; acknowledgement and retry
			// semantics are tracked separately.
			pushPortData.UpdateRealtimeJourneys(dispatcher)
		}
	}
}
