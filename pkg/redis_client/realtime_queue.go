package redis_client

import (
	"fmt"
	"sync"
	"time"

	"github.com/adjust/rmq/v5"
	"github.com/rs/zerolog/log"
)

var realtimeAdmission realtimeQueueAdmission

type realtimeQueueAdmission struct {
	sync.Mutex
	remaining int64
}

// WaitForRealtimeQueueCapacity shares short capacity grants among feed workers.
// Callers reserve per submission or small publish batch.
func WaitForRealtimeQueueCapacity(count int) error {
	return realtimeAdmission.wait(QueueConnection, int64(count))
}

func (admission *realtimeQueueAdmission) wait(connection rmq.Connection, count int64) error {
	if count == 0 {
		return nil
	}
	if connection == nil {
		return ErrQueueConnectionNotInitialized
	}
	const readyLimit = 40000
	const grantSize = 512
	admission.Lock()
	defer admission.Unlock()

	waited := false
	for admission.remaining < count {
		stats, err := connection.CollectStats([]string{"realtime-queue"})
		if err != nil {
			return fmt.Errorf("read realtime queue depth: %w", err)
		}
		ready := stats.QueueStats["realtime-queue"].ReadyCount
		if ready+count <= readyLimit {
			admission.remaining = min(grantSize, readyLimit-ready)
			break
		}
		if !waited {
			log.Info().Int64("queuesize", ready).Msg("Waiting for realtime queue capacity during import")
			waited = true
		}
		time.Sleep(time.Second)
	}
	admission.remaining -= count
	return nil
}
