package runtimestats

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/redis_client"
)

var startedAt = time.Now()

var (
	payloadsReceived            atomic.Uint64
	payloadDecodeErrors         atomic.Uint64
	nilEvents                   atomic.Uint64
	vehicleEventsReceived       atomic.Uint64
	vehicleEventsCoalesced      atomic.Uint64
	vehicleEventsSkipped        atomic.Uint64
	serviceAlertEventsReceived  atomic.Uint64
	identifiedVehicleEvents     atomic.Uint64
	unidentifiedVehicleEvents   atomic.Uint64
	realtimeJourneyUpdates      atomic.Uint64
	locationOnlyUpdates         atomic.Uint64
	realtimeProcessingErrors    atomic.Uint64
	realtimeMongoEnqueued       atomic.Uint64
	realtimeMongoFlushed        atomic.Uint64
	realtimeMongoFlushBatches   atomic.Uint64
	realtimeMongoFlushErrors    atomic.Uint64
	realtimeMongoDirectWrites   atomic.Uint64
	realtimeMongoQueueDepth     atomic.Int64
	realtimeMongoQueueCapacity  atomic.Int64
	realtimeMongoLastFlushUnix  atomic.Int64
	realtimeMongoLastFlushMs    atomic.Int64
	realtimeMongoLastFlushItems atomic.Int64
)

type Snapshot struct {
	GeneratedAt   time.Time            `json:"generated_at"`
	StartedAt     time.Time            `json:"started_at"`
	UptimeSeconds int64                `json:"uptime_seconds"`
	Counters      CounterSnapshot      `json:"counters"`
	Rates         RateSnapshot         `json:"rates_per_second"`
	MongoFlusher  MongoFlusherSnapshot `json:"mongo_flusher"`
	Redis         RedisSnapshot        `json:"redis"`
	RealtimeStore realtimestore.Stats  `json:"realtime_store"`
	Queues        map[string]QueueStat `json:"queues"`
	Go            GoSnapshot           `json:"go"`
	Errors        []string             `json:"errors,omitempty"`
}

type CounterSnapshot struct {
	PayloadsReceived           uint64 `json:"payloads_received"`
	PayloadDecodeErrors        uint64 `json:"payload_decode_errors"`
	NilEvents                  uint64 `json:"nil_events"`
	VehicleEventsReceived      uint64 `json:"vehicle_events_received"`
	VehicleEventsCoalesced     uint64 `json:"vehicle_events_coalesced"`
	VehicleEventsSkipped       uint64 `json:"vehicle_events_skipped"`
	ServiceAlertEventsReceived uint64 `json:"service_alert_events_received"`
	IdentifiedVehicleEvents    uint64 `json:"identified_vehicle_events"`
	UnidentifiedVehicleEvents  uint64 `json:"unidentified_vehicle_events"`
	RealtimeJourneyUpdates     uint64 `json:"realtime_journey_updates"`
	LocationOnlyUpdates        uint64 `json:"location_only_updates"`
	RealtimeProcessingErrors   uint64 `json:"realtime_processing_errors"`
}

type RateSnapshot struct {
	PayloadsReceived          float64 `json:"payloads_received"`
	VehicleEventsReceived     float64 `json:"vehicle_events_received"`
	VehicleEventsCoalesced    float64 `json:"vehicle_events_coalesced"`
	IdentifiedVehicleEvents   float64 `json:"identified_vehicle_events"`
	UnidentifiedVehicleEvents float64 `json:"unidentified_vehicle_events"`
	RealtimeJourneyUpdates    float64 `json:"realtime_journey_updates"`
	LocationOnlyUpdates       float64 `json:"location_only_updates"`
	MongoWritesFlushed        float64 `json:"mongo_writes_flushed"`
}

type MongoFlusherSnapshot struct {
	QueuedWrites        uint64    `json:"queued_writes"`
	FlushedWrites       uint64    `json:"flushed_writes"`
	FlushBatches        uint64    `json:"flush_batches"`
	FlushErrors         uint64    `json:"flush_errors"`
	DirectWrites        uint64    `json:"direct_writes"`
	QueueDepth          int64     `json:"queue_depth"`
	QueueCapacity       int64     `json:"queue_capacity"`
	LastFlushAt         time.Time `json:"last_flush_at,omitempty"`
	LastFlushDurationMS int64     `json:"last_flush_duration_ms"`
	LastFlushItems      int64     `json:"last_flush_items"`
}

type RedisSnapshot struct {
	Connected              bool              `json:"connected"`
	DBSize                 int64             `json:"db_size"`
	UsedMemoryBytes        int64             `json:"used_memory_bytes"`
	UsedMemoryPeakBytes    int64             `json:"used_memory_peak_bytes"`
	UsedMemoryDatasetBytes int64             `json:"used_memory_dataset_bytes"`
	MaxMemoryBytes         int64             `json:"max_memory_bytes"`
	MemoryFragmentation    float64           `json:"memory_fragmentation_ratio"`
	Pool                   RedisPoolSnapshot `json:"pool"`
}

type RedisPoolSnapshot struct {
	Hits       uint32 `json:"hits"`
	Misses     uint32 `json:"misses"`
	Timeouts   uint32 `json:"timeouts"`
	TotalConns uint32 `json:"total_connections"`
	IdleConns  uint32 `json:"idle_connections"`
	StaleConns uint32 `json:"stale_connections"`
}

type QueueStat struct {
	Ready       int64 `json:"ready"`
	Rejected    int64 `json:"rejected"`
	Unacked     int64 `json:"unacked"`
	Consumers   int64 `json:"consumers"`
	Connections int64 `json:"connections"`
}

type GoSnapshot struct {
	Goroutines      int    `json:"goroutines"`
	AllocBytes      uint64 `json:"alloc_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`
	SysBytes        uint64 `json:"sys_bytes"`
	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	HeapObjects     uint64 `json:"heap_objects"`
	NumGC           uint32 `json:"num_gc"`
	LastGCPauseNS   uint64 `json:"last_gc_pause_ns"`
}

func RecordRealtimeBatch(payloads int, decodeErrors int, nilEventCount int, vehicleEvents int, coalescedVehicleEvents int, serviceAlertEvents int, skippedVehicleEvents int) {
	payloadsReceived.Add(uint64(payloads))
	payloadDecodeErrors.Add(uint64(decodeErrors))
	nilEvents.Add(uint64(nilEventCount))
	vehicleEventsReceived.Add(uint64(vehicleEvents))
	vehicleEventsCoalesced.Add(uint64(coalescedVehicleEvents))
	serviceAlertEventsReceived.Add(uint64(serviceAlertEvents))
	vehicleEventsSkipped.Add(uint64(skippedVehicleEvents))
}

func RecordVehicleIdentified() {
	identifiedVehicleEvents.Add(1)
}

func RecordVehicleUnidentified() {
	unidentifiedVehicleEvents.Add(1)
}

func RecordRealtimeJourneyUpdate() {
	realtimeJourneyUpdates.Add(1)
}

func RecordLocationOnlyUpdate() {
	locationOnlyUpdates.Add(1)
}

func RecordRealtimeProcessingError() {
	realtimeProcessingErrors.Add(1)
}

func RecordMongoFlusherQueue(depth int, capacity int) {
	realtimeMongoQueueDepth.Store(int64(depth))
	realtimeMongoQueueCapacity.Store(int64(capacity))
}

func RecordMongoEnqueued(count int, depth int, capacity int) {
	realtimeMongoEnqueued.Add(uint64(count))
	RecordMongoFlusherQueue(depth, capacity)
}

func RecordMongoDirectWrite(count int) {
	realtimeMongoDirectWrites.Add(uint64(count))
}

func RecordMongoFlush(count int, duration time.Duration, err error) {
	if err != nil {
		realtimeMongoFlushErrors.Add(1)
		return
	}

	realtimeMongoFlushed.Add(uint64(count))
	realtimeMongoFlushBatches.Add(1)
	realtimeMongoLastFlushUnix.Store(time.Now().Unix())
	realtimeMongoLastFlushMs.Store(duration.Milliseconds())
	realtimeMongoLastFlushItems.Store(int64(count))
}

func GetSnapshot(ctx context.Context) Snapshot {
	now := time.Now()
	uptime := now.Sub(startedAt)
	if uptime < time.Second {
		uptime = time.Second
	}
	uptimeSeconds := int64(uptime.Seconds())

	counters := CounterSnapshot{
		PayloadsReceived:           payloadsReceived.Load(),
		PayloadDecodeErrors:        payloadDecodeErrors.Load(),
		NilEvents:                  nilEvents.Load(),
		VehicleEventsReceived:      vehicleEventsReceived.Load(),
		VehicleEventsCoalesced:     vehicleEventsCoalesced.Load(),
		VehicleEventsSkipped:       vehicleEventsSkipped.Load(),
		ServiceAlertEventsReceived: serviceAlertEventsReceived.Load(),
		IdentifiedVehicleEvents:    identifiedVehicleEvents.Load(),
		UnidentifiedVehicleEvents:  unidentifiedVehicleEvents.Load(),
		RealtimeJourneyUpdates:     realtimeJourneyUpdates.Load(),
		LocationOnlyUpdates:        locationOnlyUpdates.Load(),
		RealtimeProcessingErrors:   realtimeProcessingErrors.Load(),
	}

	errors := []string{}
	redisStats, redisErrors := getRedisSnapshot(ctx)
	errors = append(errors, redisErrors...)

	realtimeStoreStats, err := realtimestore.GetStats(ctx)
	if err != nil {
		errors = append(errors, err.Error())
	}

	queues, queueErrors := getQueueStats()
	errors = append(errors, queueErrors...)

	return Snapshot{
		GeneratedAt:   now,
		StartedAt:     startedAt,
		UptimeSeconds: uptimeSeconds,
		Counters:      counters,
		Rates: RateSnapshot{
			PayloadsReceived:          rate(counters.PayloadsReceived, uptimeSeconds),
			VehicleEventsReceived:     rate(counters.VehicleEventsReceived, uptimeSeconds),
			VehicleEventsCoalesced:    rate(counters.VehicleEventsCoalesced, uptimeSeconds),
			IdentifiedVehicleEvents:   rate(counters.IdentifiedVehicleEvents, uptimeSeconds),
			UnidentifiedVehicleEvents: rate(counters.UnidentifiedVehicleEvents, uptimeSeconds),
			RealtimeJourneyUpdates:    rate(counters.RealtimeJourneyUpdates, uptimeSeconds),
			LocationOnlyUpdates:       rate(counters.LocationOnlyUpdates, uptimeSeconds),
			MongoWritesFlushed:        rate(realtimeMongoFlushed.Load(), uptimeSeconds),
		},
		MongoFlusher:  getMongoFlusherSnapshot(),
		Redis:         redisStats,
		RealtimeStore: realtimeStoreStats,
		Queues:        queues,
		Go:            getGoSnapshot(),
		Errors:        errors,
	}
}

func getMongoFlusherSnapshot() MongoFlusherSnapshot {
	lastFlushUnix := realtimeMongoLastFlushUnix.Load()
	var lastFlushAt time.Time
	if lastFlushUnix > 0 {
		lastFlushAt = time.Unix(lastFlushUnix, 0)
	}

	return MongoFlusherSnapshot{
		QueuedWrites:        realtimeMongoEnqueued.Load(),
		FlushedWrites:       realtimeMongoFlushed.Load(),
		FlushBatches:        realtimeMongoFlushBatches.Load(),
		FlushErrors:         realtimeMongoFlushErrors.Load(),
		DirectWrites:        realtimeMongoDirectWrites.Load(),
		QueueDepth:          realtimeMongoQueueDepth.Load(),
		QueueCapacity:       realtimeMongoQueueCapacity.Load(),
		LastFlushAt:         lastFlushAt,
		LastFlushDurationMS: realtimeMongoLastFlushMs.Load(),
		LastFlushItems:      realtimeMongoLastFlushItems.Load(),
	}
}

func getRedisSnapshot(ctx context.Context) (RedisSnapshot, []string) {
	snapshot := RedisSnapshot{}
	errors := []string{}

	if redis_client.Client == nil {
		errors = append(errors, "redis client is not connected")
		return snapshot, errors
	}

	snapshot.Connected = true

	dbSize, err := redis_client.Client.DBSize(ctx).Result()
	if err != nil {
		errors = append(errors, err.Error())
	} else {
		snapshot.DBSize = dbSize
	}

	info, err := redis_client.Client.Info(ctx, "memory").Result()
	if err != nil {
		errors = append(errors, err.Error())
	} else {
		memoryFields := parseRedisInfo(info)
		snapshot.UsedMemoryBytes = parseInt64(memoryFields["used_memory"])
		snapshot.UsedMemoryPeakBytes = parseInt64(memoryFields["used_memory_peak"])
		snapshot.UsedMemoryDatasetBytes = parseInt64(memoryFields["used_memory_dataset"])
		snapshot.MaxMemoryBytes = parseInt64(memoryFields["maxmemory"])
		snapshot.MemoryFragmentation = parseFloat64(memoryFields["mem_fragmentation_ratio"])
	}

	poolStats := redis_client.Client.PoolStats()
	snapshot.Pool = RedisPoolSnapshot{
		Hits:       poolStats.Hits,
		Misses:     poolStats.Misses,
		Timeouts:   poolStats.Timeouts,
		TotalConns: poolStats.TotalConns,
		IdleConns:  poolStats.IdleConns,
		StaleConns: poolStats.StaleConns,
	}

	return snapshot, errors
}

func getQueueStats() (map[string]QueueStat, []string) {
	queueStats := map[string]QueueStat{}
	errors := []string{}

	if redis_client.QueueConnection == nil {
		errors = append(errors, "redis queue connection is not connected")
		return queueStats, errors
	}

	queues, err := redis_client.QueueConnection.GetOpenQueues()
	if err != nil {
		errors = append(errors, err.Error())
		return queueStats, errors
	}

	stats, err := redis_client.QueueConnection.CollectStats(queues)
	if err != nil {
		errors = append(errors, err.Error())
		return queueStats, errors
	}

	for name, stat := range stats.QueueStats {
		queueStats[name] = QueueStat{
			Ready:       stat.ReadyCount,
			Rejected:    stat.RejectedCount,
			Unacked:     stat.UnackedCount(),
			Consumers:   stat.ConsumerCount(),
			Connections: stat.ConnectionCount(),
		}
	}

	return queueStats, errors
}

func getGoSnapshot() GoSnapshot {
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)

	lastGCPause := uint64(0)
	if mem.NumGC > 0 {
		lastGCPause = mem.PauseNs[(mem.NumGC+255)%256]
	}

	return GoSnapshot{
		Goroutines:      runtime.NumGoroutine(),
		AllocBytes:      mem.Alloc,
		TotalAllocBytes: mem.TotalAlloc,
		SysBytes:        mem.Sys,
		HeapAllocBytes:  mem.HeapAlloc,
		HeapObjects:     mem.HeapObjects,
		NumGC:           mem.NumGC,
		LastGCPauseNS:   lastGCPause,
	}
}

func parseRedisInfo(info string) map[string]string {
	fields := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[key] = value
		}
	}

	return fields
}

func parseInt64(value string) int64 {
	parsed, _ := strconv.ParseInt(value, 10, 64)
	return parsed
}

func parseFloat64(value string) float64 {
	parsed, _ := strconv.ParseFloat(value, 64)
	return parsed
}

func rate(count uint64, seconds int64) float64 {
	if seconds <= 0 {
		return 0
	}

	return float64(count) / float64(seconds)
}
