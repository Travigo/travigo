package darwin

import (
	"hash/fnv"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	// DefaultWorkers is the number of independent train runs Darwin can process
	// at once. Workers mostly wait on Redis and MongoDB, so this is higher than
	// the available CPU.
	DefaultWorkers = 16
	// DefaultQueueDepth bounds queued updates across all workers. A full queue
	// blocks the STOMP reader, which is the intended backpressure.
	DefaultQueueDepth = 4096
)

// workDispatcher applies Darwin updates concurrently while preserving feed
// order for each key. The STOMP reader submits frames in arrival order, so two
// updates for the same train run always reach the same worker shard in order.
type workDispatcher interface {
	Submit(key string, work func())
}

// inlineDispatcher applies work synchronously. It keeps UpdateRealtimeJourneys
// usable without a dispatcher, for example in tests.
type inlineDispatcher struct{}

func (inlineDispatcher) Submit(_ string, work func()) {
	work()
}

type darwinDispatcher struct {
	shards    []chan func()
	wg        sync.WaitGroup
	mu        sync.RWMutex
	closed    bool
	closeOnce sync.Once

	submitted atomic.Int64
	completed atomic.Int64
	queued    atomic.Int64
}

func newDarwinDispatcher(workers, queueDepth int) *darwinDispatcher {
	if workers < 1 {
		workers = DefaultWorkers
	}
	if workers > 256 {
		workers = 256
	}
	if queueDepth < workers {
		queueDepth = workers
	}

	perShard := queueDepth / workers
	if perShard < 1 {
		perShard = 1
	}

	dispatcher := &darwinDispatcher{
		shards: make([]chan func(), workers),
	}
	for index := range dispatcher.shards {
		shard := make(chan func(), perShard)
		dispatcher.shards[index] = shard
		dispatcher.wg.Add(1)
		go dispatcher.worker(shard)
	}

	return dispatcher
}

func (d *darwinDispatcher) worker(shard <-chan func()) {
	defer d.wg.Done()

	for work := range shard {
		func() {
			defer func() {
				d.completed.Add(1)
				d.queued.Add(-1)

				if recovered := recover(); recovered != nil {
					log.Error().
						Interface("panic", recovered).
						Bytes("stack", debug.Stack()).
						Msg("Recovered from Darwin update panic")
				}
			}()

			work()
		}()
	}
}

// Submit queues work on the shard selected by key. Same-key work is always
// processed by one worker in submission order.
func (d *darwinDispatcher) Submit(key string, work func()) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return
	}

	d.queued.Add(1)
	shard := d.shards[darwinShardIndex(key, len(d.shards))]
	shard <- work
	d.submitted.Add(1)
}

func (d *darwinDispatcher) Close() {
	d.closeOnce.Do(func() {
		d.mu.Lock()
		d.closed = true
		for _, shard := range d.shards {
			close(shard)
		}
		d.mu.Unlock()

		d.wg.Wait()
	})
}

// LogStats periodically reports dispatcher depth while there is work or new
// submissions. It is intentionally silent when the feed is idle.
func (d *darwinDispatcher) LogStats(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		var lastSubmitted int64
		for range ticker.C {
			submitted := d.submitted.Load()
			if submitted == lastSubmitted && d.queued.Load() == 0 {
				continue
			}
			lastSubmitted = submitted

			log.Info().
				Int64("submitted", submitted).
				Int64("completed", d.completed.Load()).
				Int64("queued", d.queued.Load()).
				Int("workers", len(d.shards)).
				Msg("Darwin update dispatcher")
		}
	}()
}

func darwinShardIndex(key string, shards int) int {
	if shards <= 1 {
		return 0
	}

	hash := fnv.New32a()
	_, _ = hash.Write([]byte(key))
	return int(hash.Sum32() % uint32(shards))
}

func darwinRunKey(ssd, uid, rid string) string {
	if uid != "" {
		return "run:" + ssd + ":" + uid
	}
	if rid != "" {
		return "run:rid:" + rid
	}
	return "run:unknown:" + ssd
}

// darwinFormationKey keeps allocation and occupancy writes for one run on the
// same shard, in submission order.
func darwinFormationKey(rid string) string {
	return "formation:" + rid
}
