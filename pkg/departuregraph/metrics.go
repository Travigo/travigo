package departuregraph

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type LookupStats struct {
	Total                 uint64  `json:"total"`
	Hits                  uint64  `json:"hits"`
	Misses                uint64  `json:"misses"`
	HitRate               float64 `json:"hitRate"`
	LazyFills             uint64  `json:"lazyFills"`
	LazyFillFailures      uint64  `json:"lazyFillFailures"`
	LazyFillsInFlight     int64   `json:"lazyFillsInFlight"`
	AverageLazyFillMillis float64 `json:"averageLazyFillMillis"`
	MaximumLazyFillMillis float64 `json:"maximumLazyFillMillis"`
}

type BuildStats struct {
	Running                   bool       `json:"running"`
	StartedAt                 *time.Time `json:"startedAt,omitempty"`
	EstimatedJourneys         int64      `json:"estimatedJourneys"`
	ScannedJourneys           int64      `json:"scannedJourneys"`
	ActiveJourneyDays         int64      `json:"activeJourneyDays"`
	Progress                  float64    `json:"progress"`
	ElapsedMillis             float64    `json:"elapsedMillis"`
	JourneysPerSecond         float64    `json:"journeysPerSecond"`
	EstimatedRemainingSeconds float64    `json:"estimatedRemainingSeconds"`
	SuccessfulBuilds          uint64     `json:"successfulBuilds"`
	FailedBuilds              uint64     `json:"failedBuilds"`
	LastCompletedAt           *time.Time `json:"lastCompletedAt,omitempty"`
	LastDurationMillis        float64    `json:"lastDurationMillis"`
	LastError                 string     `json:"lastError,omitempty"`
}

type SnapshotStats struct {
	Writing                   bool       `json:"writing"`
	SuccessfulWrites          uint64     `json:"successfulWrites"`
	FailedWrites              uint64     `json:"failedWrites"`
	LastWriteAt               *time.Time `json:"lastWriteAt,omitempty"`
	LastWriteDurationMillis   float64    `json:"lastWriteDurationMillis"`
	FileSizeBytes             int64      `json:"fileSizeBytes"`
	LastWriteError            string     `json:"lastWriteError,omitempty"`
	RestoredAt                *time.Time `json:"restoredAt,omitempty"`
	LastRestoreDurationMillis float64    `json:"lastRestoreDurationMillis"`
	LastRestoreError          string     `json:"lastRestoreError,omitempty"`
}

type RequestStats struct {
	StartedAt                      time.Time `json:"startedAt"`
	UptimeSeconds                  float64   `json:"uptimeSeconds"`
	Total                          uint64    `json:"total"`
	Completed                      uint64    `json:"completed"`
	Failed                         uint64    `json:"failed"`
	InFlight                       int64     `json:"inFlight"`
	CompletedLastMinute            uint64    `json:"completedLastMinute"`
	FailuresLastMinute             uint64    `json:"failuresLastMinute"`
	RequestsPerSecondLastMinute    float64   `json:"requestsPerSecondLastMinute"`
	AverageLatencyMillis           float64   `json:"averageLatencyMillis"`
	AverageLatencyLastMinuteMillis float64   `json:"averageLatencyLastMinuteMillis"`
	MaximumLatencyMillis           float64   `json:"maximumLatencyMillis"`
	LastLatencyMillis              float64   `json:"lastLatencyMillis"`
}

type MemoryStats struct {
	HeapAllocBytes       uint64  `json:"heapAllocBytes"`
	HeapInUseBytes       uint64  `json:"heapInUseBytes"`
	HeapObjects          uint64  `json:"heapObjects"`
	StackInUseBytes      uint64  `json:"stackInUseBytes"`
	RuntimeSysBytes      uint64  `json:"runtimeSysBytes"`
	ProcessResidentBytes uint64  `json:"processResidentBytes,omitempty"`
	CgroupUsageBytes     uint64  `json:"cgroupUsageBytes,omitempty"`
	CgroupLimitBytes     uint64  `json:"cgroupLimitBytes,omitempty"`
	Goroutines           int     `json:"goroutines"`
	GarbageCollections   uint32  `json:"garbageCollections"`
	LastGCPauseMillis    float64 `json:"lastGCPauseMillis"`
}

type ServiceStats struct {
	Stats
	GeneratedAt time.Time
	Requests    RequestStats
	Memory      MemoryStats
}

type graphMetrics struct {
	lookups           atomic.Uint64
	hits              atomic.Uint64
	misses            atomic.Uint64
	lazyFills         atomic.Uint64
	lazyFillFailures  atomic.Uint64
	lazyFillsInFlight atomic.Int64
	lazyFillNanos     atomic.Uint64
	lazyFillMaximum   atomic.Uint64
	build             buildTracker
	snapshot          snapshotTracker
}

func (m *graphMetrics) lookup(hit bool) {
	m.lookups.Add(1)
	if hit {
		m.hits.Add(1)
	} else {
		m.misses.Add(1)
	}
}

func (m *graphMetrics) beginLazyFill() time.Time {
	m.lazyFills.Add(1)
	m.lazyFillsInFlight.Add(1)
	return time.Now()
}

func (m *graphMetrics) finishLazyFill(started time.Time, err error) {
	m.lazyFillsInFlight.Add(-1)
	if err != nil {
		m.lazyFillFailures.Add(1)
	}
	duration := uint64(time.Since(started))
	m.lazyFillNanos.Add(duration)
	setMaximum(&m.lazyFillMaximum, duration)
}

func (m *graphMetrics) lookupStats() LookupStats {
	total := m.lookups.Load()
	hits := m.hits.Load()
	fills := m.lazyFills.Load()
	result := LookupStats{
		Total:                 total,
		Hits:                  hits,
		Misses:                m.misses.Load(),
		LazyFills:             fills,
		LazyFillFailures:      m.lazyFillFailures.Load(),
		LazyFillsInFlight:     m.lazyFillsInFlight.Load(),
		MaximumLazyFillMillis: durationMillis(time.Duration(m.lazyFillMaximum.Load())),
	}
	if total > 0 {
		result.HitRate = float64(hits) / float64(total)
	}
	if fills > 0 {
		result.AverageLazyFillMillis = durationMillis(time.Duration(m.lazyFillNanos.Load() / fills))
	}
	return result
}

func setMaximum(target *atomic.Uint64, value uint64) {
	for current := target.Load(); value > current; current = target.Load() {
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}

type buildTracker struct {
	mu                sync.RWMutex
	running           bool
	startedAt         time.Time
	estimatedJourneys int64
	scannedJourneys   atomic.Int64
	activeJourneyDays atomic.Int64
	successfulBuilds  uint64
	failedBuilds      uint64
	lastCompletedAt   time.Time
	lastDuration      time.Duration
	lastError         string
}

func (b *buildTracker) begin() {
	b.mu.Lock()
	b.running = true
	b.startedAt = time.Now()
	b.estimatedJourneys = 0
	b.lastError = ""
	b.scannedJourneys.Store(0)
	b.activeJourneyDays.Store(0)
	b.mu.Unlock()
}

func (b *buildTracker) setEstimatedJourneys(total int64) {
	b.mu.Lock()
	b.estimatedJourneys = total
	b.mu.Unlock()
}

func (b *buildTracker) scanned(activeJourneyDays int64) {
	b.scannedJourneys.Add(1)
	b.activeJourneyDays.Add(activeJourneyDays)
}

func (b *buildTracker) finish(err error) {
	finished := time.Now()
	b.mu.Lock()
	b.running = false
	b.lastDuration = finished.Sub(b.startedAt)
	if err != nil {
		b.failedBuilds++
		b.lastError = err.Error()
	} else {
		b.successfulBuilds++
		b.lastCompletedAt = finished
		b.lastError = ""
	}
	b.mu.Unlock()
}

func (b *buildTracker) stats() BuildStats {
	b.mu.RLock()
	result := BuildStats{
		Running:            b.running,
		EstimatedJourneys:  b.estimatedJourneys,
		ScannedJourneys:    b.scannedJourneys.Load(),
		ActiveJourneyDays:  b.activeJourneyDays.Load(),
		SuccessfulBuilds:   b.successfulBuilds,
		FailedBuilds:       b.failedBuilds,
		LastDurationMillis: durationMillis(b.lastDuration),
		LastError:          b.lastError,
	}
	if !b.startedAt.IsZero() {
		startedAt := b.startedAt
		result.StartedAt = &startedAt
	}
	if !b.lastCompletedAt.IsZero() {
		completedAt := b.lastCompletedAt
		result.LastCompletedAt = &completedAt
	}
	b.mu.RUnlock()
	if result.EstimatedJourneys > 0 {
		result.Progress = min(1, float64(result.ScannedJourneys)/float64(result.EstimatedJourneys))
	}
	elapsed := result.LastDurationMillis / 1000
	if result.Running && result.StartedAt != nil {
		elapsed = time.Since(*result.StartedAt).Seconds()
	}
	result.ElapsedMillis = elapsed * 1000
	if elapsed > 0 {
		result.JourneysPerSecond = float64(result.ScannedJourneys) / elapsed
	}
	if result.Running && result.JourneysPerSecond > 0 && result.EstimatedJourneys > result.ScannedJourneys {
		result.EstimatedRemainingSeconds = float64(result.EstimatedJourneys-result.ScannedJourneys) / result.JourneysPerSecond
	}
	return result
}

type snapshotTracker struct {
	mu                  sync.RWMutex
	writing             bool
	successfulWrites    uint64
	failedWrites        uint64
	lastWriteAt         time.Time
	lastWriteDuration   time.Duration
	fileSizeBytes       int64
	lastWriteError      string
	restoredAt          time.Time
	lastRestoreDuration time.Duration
	lastRestoreError    string
}

func (s *snapshotTracker) beginWrite() time.Time {
	s.mu.Lock()
	s.writing = true
	s.mu.Unlock()
	return time.Now()
}

func (s *snapshotTracker) finishWrite(started time.Time, size int64, err error) {
	finished := time.Now()
	s.mu.Lock()
	s.writing = false
	s.lastWriteAt = finished
	s.lastWriteDuration = finished.Sub(started)
	if err != nil {
		s.failedWrites++
		s.lastWriteError = err.Error()
	} else {
		s.successfulWrites++
		s.fileSizeBytes = size
		s.lastWriteError = ""
	}
	s.mu.Unlock()
}

func (s *snapshotTracker) restored(started time.Time, size int64, err error) {
	finished := time.Now()
	s.mu.Lock()
	s.restoredAt = finished
	s.lastRestoreDuration = finished.Sub(started)
	s.fileSizeBytes = size
	if err != nil {
		s.lastRestoreError = err.Error()
	} else {
		s.lastRestoreError = ""
	}
	s.mu.Unlock()
}

func (s *snapshotTracker) stats() SnapshotStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := SnapshotStats{
		Writing:                   s.writing,
		SuccessfulWrites:          s.successfulWrites,
		FailedWrites:              s.failedWrites,
		LastWriteDurationMillis:   durationMillis(s.lastWriteDuration),
		FileSizeBytes:             s.fileSizeBytes,
		LastWriteError:            s.lastWriteError,
		LastRestoreDurationMillis: durationMillis(s.lastRestoreDuration),
		LastRestoreError:          s.lastRestoreError,
	}
	if !s.lastWriteAt.IsZero() {
		value := s.lastWriteAt
		result.LastWriteAt = &value
	}
	if !s.restoredAt.IsZero() {
		value := s.restoredAt
		result.RestoredAt = &value
	}
	return result
}

const requestWindowSeconds = 60

type requestBucket struct {
	second        int64
	completed     uint64
	failed        uint64
	durationNanos uint64
}

type requestTracker struct {
	startedAt     time.Time
	inFlight      atomic.Int64
	mu            sync.Mutex
	total         uint64
	completed     uint64
	failed        uint64
	durationNanos uint64
	maximumNanos  uint64
	lastNanos     uint64
	buckets       [requestWindowSeconds]requestBucket
}

func newRequestTracker() *requestTracker {
	return &requestTracker{startedAt: time.Now()}
}

func (r *requestTracker) begin() time.Time {
	r.inFlight.Add(1)
	r.mu.Lock()
	r.total++
	r.mu.Unlock()
	return time.Now()
}

func (r *requestTracker) finish(started time.Time, err bool) {
	duration := uint64(time.Since(started))
	second := time.Now().Unix()
	r.inFlight.Add(-1)
	r.mu.Lock()
	r.completed++
	r.durationNanos += duration
	r.lastNanos = duration
	if duration > r.maximumNanos {
		r.maximumNanos = duration
	}
	if err {
		r.failed++
	}
	bucket := &r.buckets[second%requestWindowSeconds]
	if bucket.second != second {
		*bucket = requestBucket{second: second}
	}
	bucket.completed++
	bucket.durationNanos += duration
	if err {
		bucket.failed++
	}
	r.mu.Unlock()
}

func (r *requestTracker) stats(now time.Time) RequestStats {
	r.mu.Lock()
	result := RequestStats{
		StartedAt:            r.startedAt,
		UptimeSeconds:        now.Sub(r.startedAt).Seconds(),
		Total:                r.total,
		Completed:            r.completed,
		Failed:               r.failed,
		InFlight:             r.inFlight.Load(),
		MaximumLatencyMillis: durationMillis(time.Duration(r.maximumNanos)),
		LastLatencyMillis:    durationMillis(time.Duration(r.lastNanos)),
	}
	if r.completed > 0 {
		result.AverageLatencyMillis = durationMillis(time.Duration(r.durationNanos / r.completed))
	}
	cutoff := now.Unix() - requestWindowSeconds + 1
	var recentDuration uint64
	for _, bucket := range r.buckets {
		if bucket.second < cutoff {
			continue
		}
		result.CompletedLastMinute += bucket.completed
		result.FailuresLastMinute += bucket.failed
		recentDuration += bucket.durationNanos
	}
	r.mu.Unlock()
	windowSeconds := min(float64(requestWindowSeconds), max(1, result.UptimeSeconds))
	result.RequestsPerSecondLastMinute = float64(result.CompletedLastMinute) / windowSeconds
	if result.CompletedLastMinute > 0 {
		result.AverageLatencyLastMinuteMillis = durationMillis(time.Duration(recentDuration / result.CompletedLastMinute))
	}
	return result
}

func currentMemoryStats() MemoryStats {
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result := MemoryStats{
		HeapAllocBytes:       memory.HeapAlloc,
		HeapInUseBytes:       memory.HeapInuse,
		HeapObjects:          memory.HeapObjects,
		StackInUseBytes:      memory.StackInuse,
		RuntimeSysBytes:      memory.Sys,
		ProcessResidentBytes: processResidentBytes(),
		CgroupUsageBytes:     readUintFile("/sys/fs/cgroup/memory.current"),
		CgroupLimitBytes:     readUintFile("/sys/fs/cgroup/memory.max"),
		Goroutines:           runtime.NumGoroutine(),
		GarbageCollections:   memory.NumGC,
	}
	if memory.NumGC > 0 {
		result.LastGCPauseMillis = durationMillis(time.Duration(memory.PauseNs[(memory.NumGC-1)%uint32(len(memory.PauseNs))]))
	}
	if result.CgroupUsageBytes == 0 {
		result.CgroupUsageBytes = readUintFile("/sys/fs/cgroup/memory/memory.usage_in_bytes")
		result.CgroupLimitBytes = readUintFile("/sys/fs/cgroup/memory/memory.limit_in_bytes")
	}
	return result
}

func processResidentBytes() uint64 {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return pages * uint64(os.Getpagesize())
}

func readUintFile(path string) uint64 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func durationMillis(value time.Duration) float64 {
	return float64(value) / float64(time.Millisecond)
}
