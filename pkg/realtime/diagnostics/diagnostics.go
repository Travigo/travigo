// Package diagnostics isolates optional payload recording from business writes.
package diagnostics

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/travigo/travigo/pkg/elastic_client"
	"github.com/travigo/travigo/pkg/realtime/telemetry"
)

const capacity = 32
const maxPayloadBytes = 256 * 1024
const writeTimeout = 250 * time.Millisecond

// One worker per sink and at most 32 immutable payloads, including in-flight work.
type sink struct {
	once                                                      sync.Once
	slots                                                     chan struct{}
	queue                                                     chan item
	seen, sampled, disabled, dropped, failed, accepted, bytes atomic.Int64
	write                                                     func(context.Context, item) error
	sample                                                    func(uint64) bool
}
type item struct {
	index   string
	payload []byte
}

func newSink(write func(context.Context, item) error) *sink {
	return &sink{slots: make(chan struct{}, capacity), queue: make(chan item, capacity), write: write, sample: func(every uint64) bool { return rand.Uint64N(every) == 0 }}
}
func (s *sink) submit(enabled bool, every uint64, index string, encode func() ([]byte, error)) {
	start := time.Now()
	defer func() { telemetry.Observe(telemetry.Diagnostics, time.Since(start)) }()
	s.seen.Add(1)
	if !enabled {
		s.disabled.Add(1)
		return
	}
	if every > 1 && !s.sample(every) {
		s.sampled.Add(1)
		return
	}
	select {
	case s.slots <- struct{}{}:
	default:
		s.dropped.Add(1)
		return
	}
	payload, err := encode()
	if err != nil || len(payload) > maxPayloadBytes {
		s.dropped.Add(1)
		<-s.slots
		return
	}
	s.once.Do(func() {
		go func() {
			for entry := range s.queue {
				ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
				err := s.write(ctx, entry)
				cancel()
				if err != nil {
					s.failed.Add(1)
				} else {
					s.accepted.Add(1)
					s.bytes.Add(int64(len(entry.payload)))
				}
				<-s.slots
			}
		}()
	})
	s.queue <- item{index: index, payload: payload}
}

var identifySink = newSink(func(ctx context.Context, i item) error {
	return elastic_client.IndexDiagnostic(ctx, i.index, bytes.NewReader(i.payload), func() { identifyAsyncFailed.Add(1) })
})
var identifyAsyncFailed atomic.Int64

func Identification(index string, success bool, document func() any) {
	every := uint64(10)
	if success {
		every = 100
	}
	identifySink.submit(elastic_client.DiagnosticsEnabled() && os.Getenv("TRAVIGO_REALTIME_IDENTIFICATION_DIAGNOSTICS") != "false", every, index, func() ([]byte, error) { return encodeIdentification(document(), every) })
}

type Counts struct {
	Seen     int64 `json:"seen"`
	Sampled  int64 `json:"sampled_out"`
	Disabled int64 `json:"disabled"`
	Dropped  int64 `json:"dropped"`
	Failed   int64 `json:"failed"`
	Accepted int64 `json:"accepted"`
	Bytes    int64 `json:"accepted_bytes"`
}

func (s *sink) snapshot() Counts {
	return Counts{s.seen.Load(), s.sampled.Load(), s.disabled.Load(), s.dropped.Load(), s.failed.Load(), s.accepted.Load(), s.bytes.Load()}
}
func Snapshot() map[string]Counts {
	r := map[string]Counts{"identification": identifySink.snapshot()}
	v := r["identification"]
	v.Failed += identifyAsyncFailed.Load()
	r["identification"] = v
	return r
}

// Historical records omit SamplingWeight and are counted with weight one.
func encodeIdentification(document any, weight uint64) ([]byte, error) {
	payload, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	if len(payload) < 2 || payload[0] != '{' {
		return nil, fmt.Errorf("identification diagnostic must be an object")
	}
	prefix := []byte(fmt.Sprintf(`{"SamplingWeight":%d,`, weight))
	if len(payload) == 2 {
		prefix = prefix[:len(prefix)-1]
	}
	return append(prefix, payload[1:]...), nil
}
