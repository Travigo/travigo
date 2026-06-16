package dataimporter

import (
	"fmt"
	"runtime"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// memoryProfiler samples runtime memory statistics while a dataset import runs
// so CPU/memory efficiency can be compared between code versions. It records the
// peak live heap (sampled periodically) and reports per-import deltas for the
// cumulative counters (allocation churn & GC), so it stays accurate even when
// the importer is run repeatedly in the same process.
type memoryProfiler struct {
	baseline runtime.MemStats

	stop chan struct{}
	done chan struct{}

	mu       sync.Mutex
	peakHeap uint64
	peakSys  uint64
}

// startMemoryProfiler snapshots a baseline and begins sampling peak memory every
// sampleInterval until Report is called.
func startMemoryProfiler(sampleInterval time.Duration) *memoryProfiler {
	p := &memoryProfiler{
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}

	runtime.ReadMemStats(&p.baseline)

	go func() {
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		defer close(p.done)

		var m runtime.MemStats
		sample := func() {
			runtime.ReadMemStats(&m)
			p.mu.Lock()
			if m.HeapAlloc > p.peakHeap {
				p.peakHeap = m.HeapAlloc
			}
			if m.Sys > p.peakSys {
				p.peakSys = m.Sys
			}
			p.mu.Unlock()
		}

		sample()
		for {
			select {
			case <-p.stop:
				sample()
				return
			case <-ticker.C:
				sample()
			}
		}
	}()

	return p
}

// Report stops sampling and logs a summary of memory & GC usage over the run.
//
//	peak_heap   - high-water mark of live heap (memory efficiency)
//	peak_sys    - high-water mark of memory obtained from the OS
//	total_alloc - cumulative bytes allocated during the run (allocation churn)
//	heap_objects - number of heap objects allocated during the run
//	num_gc      - GC cycles triggered during the run
//	gc_pause    - total stop-the-world GC pause time during the run
func (p *memoryProfiler) Report(duration time.Duration) {
	close(p.stop)
	<-p.done

	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	p.mu.Lock()
	peakHeap := p.peakHeap
	peakSys := p.peakSys
	p.mu.Unlock()

	log.Info().
		Str("duration", duration.String()).
		Str("peak_heap", formatBytes(peakHeap)).
		Str("peak_sys", formatBytes(peakSys)).
		Str("total_alloc", formatBytes(m.TotalAlloc-p.baseline.TotalAlloc)).
		Uint64("heap_objects", m.Mallocs-p.baseline.Mallocs).
		Uint32("num_gc", m.NumGC-p.baseline.NumGC).
		Str("gc_pause", time.Duration(m.PauseTotalNs-p.baseline.PauseTotalNs).String()).
		Msg("Import memory profile")
}

func formatBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.2f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}
