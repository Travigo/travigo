package redis_client

import (
	"errors"
	"testing"

	"github.com/adjust/rmq/v5"
)

type realtimeQueueStatsStub struct {
	rmq.Connection
	ready int64
	calls int
	err   error
}

func (connection *realtimeQueueStatsStub) CollectStats([]string) (rmq.Stats, error) {
	connection.calls++
	stats := rmq.NewStats()
	stats.QueueStats["realtime-queue"] = rmq.NewQueueStat(connection.ready, 0)
	return stats, connection.err
}

func TestRealtimeQueueAdmissionRechecksBeforePublishing(t *testing.T) {
	connection := &realtimeQueueStatsStub{ready: 39998}
	var admission realtimeQueueAdmission
	for range 2 {
		if err := admission.wait(connection, 1); err != nil {
			t.Fatal(err)
		}
	}
	if connection.calls != 1 {
		t.Fatalf("got %d queue checks for two spots in the shared grant", connection.calls)
	}

	connection.err = errors.New("Redis unavailable")
	if err := admission.wait(connection, 1); !errors.Is(err, connection.err) || connection.calls != 2 {
		t.Fatalf("exhausted grant did not recheck the queue: %v, %d calls", err, connection.calls)
	}

	connection.err = nil
	connection.ready = 39000
	if err := admission.wait(connection, 128); err != nil || admission.remaining != 384 {
		t.Fatalf("batch grant left %d slots, error: %v", admission.remaining, err)
	}
}
