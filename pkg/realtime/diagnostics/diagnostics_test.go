package diagnostics

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func drain(t *testing.T, s *sink) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for len(s.slots) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("worker did not drain")
		}
		time.Sleep(time.Millisecond)
	}
	close(s.queue)
}
func TestDisabledAndSamplingSkipEncoding(t *testing.T) {
	s := newSink(func(context.Context, item) error { return nil })
	var encoded atomic.Int64
	encode := func() ([]byte, error) { encoded.Add(1); return []byte("{}"), nil }
	for i := 0; i < 1000; i++ {
		s.submit(false, 1, "", encode)
	}
	if encoded.Load() != 0 || s.snapshot().Disabled != 1000 {
		t.Fatal("disabled telemetry encoded payloads")
	}
	// Isolate sampling from disabled observations.
	s = newSink(func(context.Context, item) error { return nil })
	// Use a deterministic selector to verify sampling without a probabilistic test.
	next := uint64(0)
	s.sample = func(every uint64) bool { defer func() { next++ }(); return next%every == 0 }

	for i := 0; i < 100; i++ {
		s.submit(true, 10, "", encode)
	}
	drain(t, s)
	if encoded.Load() != 10 || s.snapshot().Sampled != 90 {
		t.Fatalf("incorrect sampling: %v", s.snapshot())
	}
}
func TestSlowSinkBoundsWorkAndDropsBeforeEncoding(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var writes atomic.Int64
	s := newSink(func(context.Context, item) error {
		if writes.Add(1) == 1 {
			close(entered)
		}
		<-release
		return nil
	})
	encoded := 0
	encode := func() ([]byte, error) { encoded++; return []byte("{}"), nil }
	s.submit(true, 1, "", encode)
	<-entered
	started := time.Now()
	for i := 0; i < 1000; i++ {
		s.submit(true, 1, "", encode)
	}
	if time.Since(started) > time.Second {
		t.Fatal("producer blocked on sink")
	}
	if encoded != capacity || len(s.slots) != capacity || s.snapshot().Dropped != 1001-capacity {
		t.Fatalf("unbounded backlog: encoded=%d stats=%v", encoded, s.snapshot())
	}
	close(release)
	drain(t, s)
	if s.snapshot().Accepted != capacity {
		t.Fatal("admitted writes lost")
	}
}
func TestDiagnosticDeadlineAndEncodingFailures(t *testing.T) {
	s := newSink(func(ctx context.Context, _ item) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("missing diagnostic deadline")
		}
		<-ctx.Done()
		return ctx.Err()
	})
	s.submit(true, 1, "", func() ([]byte, error) { return nil, errors.New("encode failed") })
	s.submit(true, 1, "", func() ([]byte, error) { return make([]byte, maxPayloadBytes+1), nil })
	s.submit(true, 1, "", func() ([]byte, error) { return []byte("{}"), nil })
	drain(t, s)
	got := s.snapshot()
	if got.Dropped != 2 || got.Failed != 1 || got.Accepted != 0 {
		t.Fatalf("losses not counted: %v", got)
	}
}
func BenchmarkDiagnosticProducer(b *testing.B) {
	for _, enabled := range []bool{false, true} {
		name := "disabled"
		if enabled {
			name = "slow_sink_saturated"
		}
		b.Run(name, func(b *testing.B) {
			release := make(chan struct{})
			s := newSink(func(context.Context, item) error { <-release; return nil })
			defer func() { close(release); close(s.queue) }()
			if enabled {
				for i := 0; i < capacity; i++ {
					s.submit(true, 1, "", func() ([]byte, error) { return []byte("{}"), nil })
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				s.submit(enabled, 1, "", func() ([]byte, error) { return []byte("{}"), nil })
			}
		})
	}
}

func TestIdentificationSamplingWeightPreservesPayload(t *testing.T) {
	for _, weight := range []uint64{10, 100} {
		payload, err := encodeIdentification(struct {
			Success  bool
			Operator string
		}{weight == 100, "operator"}, weight)
		if err != nil {
			t.Fatal(err)
		}
		var got struct {
			Success        bool
			Operator       string
			SamplingWeight uint64
		}
		if err := json.Unmarshal(payload, &got); err != nil {
			t.Fatal(err)
		}
		if got.SamplingWeight != weight || got.Operator != "operator" || got.Success != (weight == 100) {
			t.Fatalf("wrong weighted payload: %s", payload)
		}
	}
	if _, err := encodeIdentification(make(chan int), 10); err == nil {
		t.Fatal("encoding error discarded")
	}
	if _, err := encodeIdentification(nil, 10); err == nil {
		t.Fatal("non-object accepted")
	}
}
