package redis_client

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
)

const realtimeQueueReadyLimit = 40000

// Feed workers share a short-lived queue depth sample but wait independently.
var realtimeQueueState realtimeQueueDepth

type realtimeQueueDepth struct {
	mu        sync.Mutex
	ready     int64
	checkedAt time.Time
}

func (depth *realtimeQueueDepth) readyCount(connection rmq.Connection, now time.Time) (int64, error) {
	if connection == nil {
		return 0, ErrQueueConnectionNotInitialized
	}
	depth.mu.Lock()
	defer depth.mu.Unlock()
	if now.Before(depth.checkedAt.Add(time.Second)) {
		return depth.ready, nil
	}
	stats, err := connection.CollectStats([]string{"realtime-queue"})
	if err != nil {
		return 0, fmt.Errorf("read realtime queue depth: %w", err)
	}
	depth.ready = stats.QueueStats["realtime-queue"].ReadyCount
	depth.checkedAt = now
	return depth.ready, nil
}

// WaitForRealtimeQueueDrain delays the next feed poll while ready work is high.
func WaitForRealtimeQueueDrain(ctx context.Context) error {
	waited := false
	for ctx.Err() == nil {
		ready, err := realtimeQueueState.readyCount(QueueConnection, time.Now())
		if err != nil || ready < realtimeQueueReadyLimit {
			return err
		}
		if !waited {
			log.Info().Int64("queuesize", ready).Msg("Waiting for realtime queue to drain before next poll")
			waited = true
		}

		timer := time.NewTimer(time.Duration(30+rand.IntN(20)) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
		case <-timer.C:
		}
	}
	return ctx.Err()
}
