package redis_client

import (
	"errors"
	"testing"
	"time"

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

func TestRealtimeQueueDepthSharesRecentSamplesAndRetriesErrors(t *testing.T) {
	now := time.Now()
	connection := &realtimeQueueStatsStub{ready: realtimeQueueReadyLimit}
	var depth realtimeQueueDepth
	assertReady := func(at time.Time, want int64, wantCalls int) {
		t.Helper()
		got, err := depth.readyCount(connection, at)
		if err != nil || got != want || connection.calls != wantCalls {
			t.Fatalf("depth = %d, error = %v, checks = %d, want %d and %d checks", got, err, connection.calls, want, wantCalls)
		}
	}
	assertReady(now, realtimeQueueReadyLimit, 1)
	connection.ready = 1000
	assertReady(now.Add(500*time.Millisecond), realtimeQueueReadyLimit, 1)

	next := now.Add(time.Second)
	connection.err = errors.New("Redis unavailable")
	if _, err := depth.readyCount(connection, next); !errors.Is(err, connection.err) {
		t.Fatalf("failed queue check returned %v", err)
	}
	connection.err = nil
	assertReady(next, 1000, 3)
}
