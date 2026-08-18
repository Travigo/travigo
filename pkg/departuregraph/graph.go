package departuregraph

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"golang.org/x/sync/singleflight"
)

const snapshotVersion = 8

const (
	maximumPendingStopFills     = 4096
	asyncFillInsertionBatchSize = 64
)

type stringID uint32
type journeyID uint32
type dayKey int32

type journeyKey struct {
	PrimaryID stringID
}

type journeyDayKey struct {
	Day     dayKey
	Journey journeyID
}

type bucketKey struct {
	Day     dayKey
	StopRef stringID
}

// patternDepartureBucket points into compact global group and entry-index
// arrays. The original departure entries remain stored only once in
// graphData.Departures.
type patternDepartureBucket struct {
	GroupStart uint32
	GroupCount uint32
}

type patternDepartureGroup struct {
	Pattern    uint32
	IndexStart uint32
	IndexCount uint32
}

// departureEntry packs the journey, boarding path and departure time into one
// word. The previous index stored only a journey ID, forcing every planner and
// board lookup to scan that journey's entire path again to rediscover where it
// boarded.
type departureEntry uint64

const (
	departureJourneyBits = 28
	departurePathBits    = 16
	departureTimeBits    = 20
	departureTimeBias    = 1 << (departureTimeBits - 1)
	departureJourneyMask = 1<<departureJourneyBits - 1
	departurePathMask    = 1<<departurePathBits - 1
	departureTimeMask    = 1<<departureTimeBits - 1
)

func packDepartureEntry(journey journeyID, pathIndex uint32, departureSeconds int32) (departureEntry, bool) {
	encodedTime := int64(departureSeconds) + departureTimeBias
	if uint64(journey) > departureJourneyMask || uint64(pathIndex) > departurePathMask || encodedTime < 0 || encodedTime > departureTimeMask {
		return 0, false
	}
	return departureEntry(uint64(journey)<<uint(departurePathBits+departureTimeBits) |
		uint64(pathIndex)<<departureTimeBits | uint64(encodedTime)), true
}

func (entry departureEntry) journey() journeyID {
	return journeyID(uint64(entry) >> uint(departurePathBits+departureTimeBits))
}

func (entry departureEntry) pathIndex() uint32 {
	return uint32(uint64(entry)>>departureTimeBits) & departurePathMask
}

func (entry departureEntry) departureSeconds() int32 {
	return int32(uint64(entry)&departureTimeMask) - departureTimeBias
}

type journeyRecord struct {
	PrimaryID          stringID
	ServiceRef         stringID
	OperatorRef        stringID
	DepartureTimezone  stringID
	DestinationDisplay stringID
	DatasetID          stringID
	BlockNumber        stringID
	DepartureSeconds   int32
	InitialArrival     int32
	PathStart          uint32
	PathCount          uint32
	ReplacementStart   uint32
	ReplacementCount   uint32
	Flags              uint8
}

type pathRecord struct {
	OriginStopRef       stringID
	DestinationStopRef  stringID
	OriginPlatform      stringID
	DestinationDisplay  stringID
	OriginDeparture     int32
	DestinationArrival  int32
	OriginActivity      uint8
	DestinationActivity uint8
}

type staticPatternRecord struct {
	StopStart  uint32
	StopCount  uint32
	ServiceRef stringID
}

type graphData struct {
	mu sync.RWMutex

	Strings                 []string
	StringIDs               map[string]stringID
	StopIDs                 map[string]stringID
	StopIdentifiers         []stopIdentifierRecord
	StopIndexByStringID     []uint32
	Stops                   []stopRecord
	StopAliasOffsets        []uint32
	StopAliases             []stringID
	StopGrid                map[spatialCell][]uint32
	TransferOffsets         []uint32
	Transfers               []transferRecord
	TransferRestrictions    []transferRestriction
	TopologyReady           bool
	ReverseTransferOffsets  []uint32
	ReverseTransferOrigins  []uint32
	ArrivalPatternOffsets   []uint32
	ArrivalPatterns         []uint64
	StaticPatterns          []staticPatternRecord
	StaticPatternStops      []uint32
	JourneyPatterns         []uint32
	PatternDepartureBuckets map[bucketKey]patternDepartureBucket
	PatternDepartureGroups  []patternDepartureGroup
	PatternDepartureIndexes []uint32
	StaticRoutingReady      bool
	Journeys                []journeyRecord
	Paths                   []pathRecord
	Replacements            []stringID
	JourneyIDs              map[journeyKey]journeyID
	JourneyDays             map[journeyDayKey]bool
	DayJourneys             map[dayKey][]journeyID
	Departures              map[bucketKey][]departureEntry
	CompleteStops           map[bucketKey]bool
	CompleteDays            map[dayKey]bool
	ScanDays                []dayKey
	ScanCursor              string
	ScanProcessed           int64
	ScanActive              int64

	IncomingJourneyStateStops []bool

	corridorMu    sync.Mutex
	corridors     map[corridorKey]*corridorCacheEntry
	corridorClock uint64
}

type Loader interface {
	LoadStopJourneys(ctx context.Context, stopRefs []string, serviceDate time.Time) ([]*ctdf.Journey, error)
	ScanJourneys(ctx context.Context, serviceDates []time.Time, after string, visit func(*ctdf.Journey, string) error) error
}

// TopologyLoader supplies the non-timetable edges and canonical stop nodes used
// by the in-memory journey planner. Implementations load this once per graph
// generation; planning requests never query the backing database.
type TopologyLoader interface {
	ScanStops(ctx context.Context, visit func(*ctdf.Stop) error) error
	ScanTransfers(ctx context.Context, visit func(*ctdf.StopTransfer) error) error
}

type JourneyCounter interface {
	JourneyCount(ctx context.Context, serviceDates []time.Time) (int64, error)
}

// Provider is the query boundary used by departure-board consumers. Graph
// implements it in the graph service and Client implements it in web-api.
type Provider interface {
	JourneysForStopWindow(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time, notBefore time.Time, limit int) ([]*ctdf.Journey, error)
}

type Config struct {
	Enabled           bool
	SnapshotPath      string
	BackgroundEnabled bool
	DaysBehind        int
	DaysAhead         int
	BatchSize         int
	BatchPause        time.Duration
	InitialBuildDelay time.Duration
	RefreshInterval   time.Duration
	RetryInterval     time.Duration
	SnapshotInterval  time.Duration
}

func DefaultConfig() Config {
	return Config{
		Enabled:           true,
		BackgroundEnabled: true,
		DaysBehind:        1,
		DaysAhead:         1,
		BatchSize:         1000,
		BatchPause:        250 * time.Millisecond,
		InitialBuildDelay: 30 * time.Second,
		RefreshInterval:   24 * time.Hour,
		RetryInterval:     time.Minute,
		SnapshotInterval:  15 * time.Minute,
	}
}

func ConfigFromEnvironment(env map[string]string) Config {
	config := DefaultConfig()
	config.Enabled = envBool(env, "TRAVIGO_DEPARTURE_GRAPH_ENABLED", config.Enabled)
	config.BackgroundEnabled = envBool(env, "TRAVIGO_DEPARTURE_GRAPH_BACKGROUND_ENABLED", config.BackgroundEnabled)
	config.SnapshotPath = strings.TrimSpace(env["TRAVIGO_DEPARTURE_GRAPH_SNAPSHOT_PATH"])
	config.DaysBehind = envInt(env, "TRAVIGO_DEPARTURE_GRAPH_DAYS_BEHIND", config.DaysBehind)
	config.DaysAhead = envInt(env, "TRAVIGO_DEPARTURE_GRAPH_DAYS_AHEAD", config.DaysAhead)
	config.BatchSize = envInt(env, "TRAVIGO_DEPARTURE_GRAPH_BATCH_SIZE", config.BatchSize)
	config.BatchPause = envDuration(env, "TRAVIGO_DEPARTURE_GRAPH_BATCH_PAUSE", config.BatchPause)
	config.InitialBuildDelay = envDuration(env, "TRAVIGO_DEPARTURE_GRAPH_INITIAL_BUILD_DELAY", config.InitialBuildDelay)
	config.RefreshInterval = envDuration(env, "TRAVIGO_DEPARTURE_GRAPH_REFRESH_INTERVAL", config.RefreshInterval)
	config.RetryInterval = envDuration(env, "TRAVIGO_DEPARTURE_GRAPH_RETRY_INTERVAL", config.RetryInterval)
	config.SnapshotInterval = envDuration(env, "TRAVIGO_DEPARTURE_GRAPH_SNAPSHOT_INTERVAL", config.SnapshotInterval)
	if config.DaysBehind < 0 {
		config.DaysBehind = 0
	}
	if config.DaysAhead < 0 {
		config.DaysAhead = 0
	}
	if config.BatchSize <= 0 {
		config.BatchSize = 1000
	}
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = 24 * time.Hour
	}
	if config.RetryInterval <= 0 {
		config.RetryInterval = time.Minute
	}
	if config.SnapshotInterval <= 0 {
		config.SnapshotInterval = 15 * time.Minute
	}
	return config
}

type Graph struct {
	loader Loader
	config Config

	current atomic.Pointer[graphData]
	fills   singleflight.Group
	pending sync.Map
	metrics graphMetrics

	snapshotMu          sync.Mutex
	completeSnapshot    atomic.Bool
	snapshotRevision    atomic.Uint64
	snapshotSaved       atomic.Uint64
	fillQueueMu         sync.Mutex
	fillQueue           []*pendingStopFill
	fillWorkerRunning   bool
	beforeApplyLazyFill func()
}

func New(loader Loader, config Config) *Graph {
	defaults := DefaultConfig()
	if config.BatchSize <= 0 {
		config.BatchSize = defaults.BatchSize
	}
	if config.RefreshInterval <= 0 {
		config.RefreshInterval = defaults.RefreshInterval
	}
	if config.RetryInterval <= 0 {
		config.RetryInterval = defaults.RetryInterval
	}
	if config.SnapshotInterval <= 0 {
		config.SnapshotInterval = defaults.SnapshotInterval
	}
	graph := &Graph{loader: loader, config: config}
	graph.current.Store(newGraphData())
	return graph
}

func newGraphData() *graphData {
	return &graphData{
		Strings:       []string{""},
		StringIDs:     map[string]stringID{"": 0},
		StopIDs:       map[string]stringID{"": 0},
		StopGrid:      map[spatialCell][]uint32{},
		JourneyIDs:    map[journeyKey]journeyID{},
		JourneyDays:   map[journeyDayKey]bool{},
		DayJourneys:   map[dayKey][]journeyID{},
		Departures:    map[bucketKey][]departureEntry{},
		CompleteStops: map[bucketKey]bool{},
		CompleteDays:  map[dayKey]bool{},
	}
}

func (g *Graph) Start(ctx context.Context) {
	if g == nil || !g.config.Enabled {
		return
	}
	restored := false
	if g.config.SnapshotPath != "" {
		if err := g.restoreTracked(g.config.SnapshotPath); err != nil {
			log.Warn().Err(err).Str("path", g.config.SnapshotPath).Msg("Departure graph snapshot restore failed; continuing with lazy fills")
		} else {
			restored = g.Stats().Journeys > 0 && g.current.Load().topologyReady()
		}
	}
	go g.run(ctx, restored)
	if g.config.SnapshotPath != "" {
		go g.runSnapshotter(ctx)
	}
}

func (g *Graph) runSnapshotter(ctx context.Context) {
	ticker := time.NewTicker(g.config.SnapshotInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := g.Save(); err != nil {
				log.Error().Err(err).Str("path", g.config.SnapshotPath).Msg("Departure graph checkpoint save failed")
			}
		}
	}
}

func (g *Graph) run(ctx context.Context, restored bool) {
	if !g.config.BackgroundEnabled || g.loader == nil {
		return
	}
	delay := g.config.InitialBuildDelay
	if g.Stats().Journeys > 0 && !g.current.Load().topologyReady() {
		delay = 0
	}
	if restored && g.current.Load().coversRolling(time.Now(), g.config.DaysBehind, g.config.DaysAhead) {
		delay = g.config.RefreshInterval
	}
	if delay > 0 {
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}

	for {
		started := time.Now()
		err := g.rebuildRolling(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			log.Error().Err(err).Dur("retry_in", g.config.RetryInterval).Msg("Departure graph background rebuild failed")
			if g.config.SnapshotPath != "" {
				if snapshotErr := g.Save(); snapshotErr != nil {
					log.Error().Err(snapshotErr).Str("path", g.config.SnapshotPath).Msg("Departure graph failed-build checkpoint save failed")
				}
			}
		}
		wait := g.waitAfterBuild(err, time.Since(started))
		if wait < time.Minute {
			wait = time.Minute
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (g *Graph) waitAfterBuild(err error, duration time.Duration) time.Duration {
	if err != nil {
		return g.config.RetryInterval
	}
	return g.config.RefreshInterval - duration
}

type pendingStopFill struct {
	fillKey   string
	day       dayKey
	canonical string
	journeys  []*ctdf.Journey
}

func (g *Graph) JourneysForStop(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time) ([]*ctdf.Journey, error) {
	return g.JourneysForStopWindow(ctx, stop, serviceDate, time.Time{}, 0)
}

func (g *Graph) JourneysForStopWindow(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time, notBefore time.Time, limit int) ([]*ctdf.Journey, error) {
	return g.journeysForStopWindow(ctx, stop, serviceDate, notBefore, limit)
}

func (g *Graph) journeysForStopWindow(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time, notBefore time.Time, limit int) ([]*ctdf.Journey, error) {
	if g == nil || g.loader == nil || stop == nil {
		return nil, fmt.Errorf("departure graph is not configured")
	}
	if limit < 0 {
		limit = 0
	}

	day := makeDayKey(serviceDate)
	data := g.current.Load()
	stopRefs := stop.GetAllStopIDs()
	if len(stopRefs) == 0 {
		return nil, nil
	}
	canonical := stop.PrimaryIdentifier
	if canonical == "" {
		canonical = stopRefs[0]
	}
	complete := data.stopComplete(day, canonical)
	g.metrics.lookup(complete)
	if complete {
		return data.materializeStopWindow(day, stopRefs, notBefore, limit), nil
	}

	fillKey := strconv.Itoa(int(day)) + "\x00" + canonical
	if pending, exists := g.pending.Load(fillKey); exists {
		return filterJourneyWindow(pending.(*pendingStopFill).journeys, stopRefs, serviceDate, notBefore, limit), nil
	}

	value, err, _ := g.fills.Do(fillKey, func() (any, error) {
		active := g.current.Load()
		if active.stopComplete(day, canonical) {
			return nil, nil
		}
		if pending, exists := g.pending.Load(fillKey); exists {
			return pending, nil
		}
		started := g.metrics.beginLazyFill()
		var fillErr error
		defer func() { g.metrics.finishLazyFill(started, fillErr) }()
		journeys, fillErr := g.loader.LoadStopJourneys(ctx, stopRefs, serviceDate)
		if fillErr != nil {
			return nil, fillErr
		}
		pending := &pendingStopFill{
			fillKey:   fillKey,
			day:       day,
			canonical: canonical,
			journeys:  journeys,
		}
		g.pending.Store(fillKey, pending)
		g.enqueuePendingStopFill(pending)
		return pending, nil
	})
	if err != nil {
		return nil, err
	}
	if value != nil {
		return filterJourneyWindow(value.(*pendingStopFill).journeys, stopRefs, serviceDate, notBefore, limit), nil
	}

	return g.current.Load().materializeStopWindow(day, stopRefs, notBefore, limit), nil
}

func (g *Graph) enqueuePendingStopFill(pending *pendingStopFill) {
	g.fillQueueMu.Lock()
	if len(g.fillQueue) >= maximumPendingStopFills {
		g.fillQueueMu.Unlock()
		g.pending.Delete(pending.fillKey)
		log.Warn().
			Str("stop", pending.canonical).
			Str("service_date", dayKeyDate(pending.day, time.UTC).Format(ctdf.YearMonthDayFormat)).
			Msg("Departure graph asynchronous fill queue is full; skipping insertion")
		return
	}
	g.fillQueue = append(g.fillQueue, pending)
	if g.fillWorkerRunning {
		g.fillQueueMu.Unlock()
		return
	}
	g.fillWorkerRunning = true
	g.fillQueueMu.Unlock()
	go g.runPendingStopFills()
}

func (g *Graph) runPendingStopFills() {
	for {
		g.fillQueueMu.Lock()
		if len(g.fillQueue) == 0 {
			g.fillWorkerRunning = false
			g.fillQueueMu.Unlock()
			return
		}
		pending := g.fillQueue[0]
		g.fillQueue[0] = nil
		g.fillQueue = g.fillQueue[1:]
		g.fillQueueMu.Unlock()

		g.applyPendingStopFill(pending)
	}
}

func (g *Graph) applyPendingStopFill(pending *pendingStopFill) {
	defer g.pending.Delete(pending.fillKey)
	if g.beforeApplyLazyFill != nil {
		g.beforeApplyLazyFill()
	}
	active := g.current.Load()
	if active.stopComplete(pending.day, pending.canonical) {
		return
	}
	for start := 0; start < len(pending.journeys); start += asyncFillInsertionBatchSize {
		end := start + asyncFillInsertionBatchSize
		if end > len(pending.journeys) {
			end = len(pending.journeys)
		}
		active.addJourneys(pending.day, pending.journeys[start:end])
	}
	active.markStopComplete(pending.day, pending.canonical)
	g.snapshotRevision.Add(1)
	log.Debug().
		Str("stop", pending.canonical).
		Str("service_date", dayKeyDate(pending.day, time.UTC).Format(ctdf.YearMonthDayFormat)).
		Int("journeys", len(pending.journeys)).
		Msg("Departure graph filled requested stop")
}

func (g *Graph) rebuildRolling(ctx context.Context, now time.Time) (err error) {
	g.snapshotRevision.Add(1)
	dates := rollingDates(now, g.config.DaysBehind, g.config.DaysAhead)
	next := g.current.Load()
	configured, matching, cursor, processed, active := next.scanState(dates)
	if configured && !matching {
		// A stale partial refresh must not replace the last complete generation.
		// Discard only its progress marker; appended journey records are compacted
		// after the next successful rolling scan.
		cursor, processed, active = "", 0, 0
	}
	if !configured || !matching {
		next.setScanProgress(dates, cursor, processed, active)
	}
	if !next.topologyReady() {
		if topologyLoader, ok := g.loader.(TopologyLoader); ok {
			if err := next.loadTopology(ctx, topologyLoader); err != nil {
				return err
			}
		}
	}
	missingDates := next.missingRollingDates(dates)
	g.metrics.build.begin(processed, active)
	defer func() { g.metrics.build.finish(err) }()
	if counter, ok := g.loader.(JourneyCounter); ok {
		if total, countErr := counter.JourneyCount(ctx, missingDates); countErr == nil {
			g.metrics.build.setEstimatedJourneys(total)
		}
	}
	processedThisRun := int64(0)
	latestCursor := cursor
	started := time.Now()
	err = g.loader.ScanJourneys(ctx, missingDates, cursor, func(journey *ctdf.Journey, journeyCursor string) error {
		processed++
		processedThisRun++
		activeForJourney := int64(0)
		if journey != nil && journey.Availability != nil {
			for _, serviceDate := range missingDates {
				if journey.Availability.MatchDate(serviceDate) {
					next.addJourney(makeDayKey(serviceDate), journey)
					activeForJourney++
				}
			}
		}
		active += activeForJourney
		g.metrics.build.scanned(activeForJourney)
		latestCursor = journeyCursor
		if processedThisRun%int64(g.config.BatchSize) == 0 {
			next.setScanProgress(dates, latestCursor, processed, active)
		}
		if processedThisRun%int64(g.config.BatchSize) == 0 && g.config.BatchPause > 0 {
			timer := time.NewTimer(g.config.BatchPause)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
		if processed%100000 == 0 {
			stats := next.stats()
			log.Info().
				Int64("scanned_journeys", processed).
				Int("stored_journeys", stats.Journeys).
				Int("stored_paths", stats.Paths).
				Msg("Departure graph background rebuild progress")
		}
		return nil
	})
	next.setScanProgress(dates, latestCursor, processed, active)
	if err != nil {
		return err
	}
	next.completeRollingScan(dates)

	if g.config.SnapshotPath != "" {
		if snapshotErr := g.saveTracked(g.config.SnapshotPath, next); snapshotErr != nil {
			log.Error().Err(snapshotErr).Str("path", g.config.SnapshotPath).Msg("Departure graph snapshot save failed")
		}
	}
	stats := next.stats()
	log.Info().
		Int64("scanned_journeys", processed).
		Int64("scanned_this_run", processedThisRun).
		Int64("active_journey_days", active).
		Int("stored_journeys", stats.Journeys).
		Int("stored_paths", stats.Paths).
		Int("departure_buckets", stats.DepartureBuckets).
		Int("arrival_buckets", stats.ArrivalBuckets).
		Dur("duration", time.Since(started)).
		Msg("Departure graph rolling rebuild complete")
	return nil
}

func (d *graphData) coversRolling(now time.Time, behind int, ahead int) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	for _, serviceDate := range rollingDates(now, behind, ahead) {
		if !d.CompleteDays[makeDayKey(serviceDate)] {
			return false
		}
	}
	return true
}

func (d *graphData) hasCompleteDays() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.CompleteDays) > 0
}

func (d *graphData) snapshotComplete() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.TopologyReady && d.StaticRoutingReady && len(d.CompleteDays) > 0 && len(d.ScanDays) == 0
}

func (d *graphData) scanState(dates []time.Time) (configured bool, matching bool, cursor string, processed int64, active int64) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if len(d.ScanDays) == 0 {
		return false, false, "", 0, 0
	}
	if len(d.ScanDays) != len(dates) {
		return true, false, "", 0, 0
	}
	for index, serviceDate := range dates {
		if d.ScanDays[index] != makeDayKey(serviceDate) {
			return true, false, "", 0, 0
		}
	}
	return true, true, d.ScanCursor, d.ScanProcessed, d.ScanActive
}

func (d *graphData) setScanProgress(dates []time.Time, cursor string, processed int64, active int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ScanDays = d.ScanDays[:0]
	for _, serviceDate := range dates {
		d.ScanDays = append(d.ScanDays, makeDayKey(serviceDate))
	}
	d.ScanCursor = cursor
	d.ScanProcessed = processed
	d.ScanActive = active
}

func (d *graphData) completeScan(dates []time.Time) {
	d.completeRollingScan(dates)
}

func (d *graphData) missingRollingDates(dates []time.Time) []time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	missing := make([]time.Time, 0, len(dates))
	for _, serviceDate := range dates {
		if !d.CompleteDays[makeDayKey(serviceDate)] {
			missing = append(missing, serviceDate)
		}
	}
	return missing
}

func (d *graphData) completeRollingScan(dates []time.Time) {
	d.mu.Lock()
	defer d.mu.Unlock()
	keepDays := make(map[dayKey]bool, len(dates))
	for _, serviceDate := range dates {
		day := makeDayKey(serviceDate)
		keepDays[day] = true
		d.CompleteDays[day] = true
	}
	for day := range d.CompleteDays {
		if !keepDays[day] {
			delete(d.CompleteDays, day)
		}
	}
	d.compactRollingDaysLocked(keepDays)
	d.sortDepartureBucketsLocked()
	d.ScanDays = nil
	d.ScanCursor = ""
	d.ScanProcessed = 0
	d.ScanActive = 0
	d.buildStaticRoutingIndexesLocked()
	d.sealBuildIndexesLocked()
}

func (d *graphData) sortDepartureBucketsLocked() {
	for key, entries := range d.Departures {
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].departureSeconds() != entries[j].departureSeconds() {
				return entries[i].departureSeconds() < entries[j].departureSeconds()
			}
			if entries[i].journey() != entries[j].journey() {
				return entries[i].journey() < entries[j].journey()
			}
			return entries[i].pathIndex() < entries[j].pathIndex()
		})
		d.Departures[key] = entries
	}
}

func (d *graphData) compactRollingDaysLocked(keepDays map[dayKey]bool) {
	if len(d.Journeys) == 0 || len(d.DayJourneys) == 0 {
		for key := range d.Departures {
			if !keepDays[key.Day] {
				delete(d.Departures, key)
			}
		}
		return
	}

	active := make([]bool, len(d.Journeys))
	for day, journeys := range d.DayJourneys {
		if !keepDays[day] {
			delete(d.DayJourneys, day)
			continue
		}
		for _, journey := range journeys {
			if int(journey) < len(active) {
				active[journey] = true
			}
		}
	}

	remap := make([]journeyID, len(d.Journeys))
	nextJourney, nextPath, nextReplacement := 0, uint32(0), uint32(0)
	for oldIndex, record := range d.Journeys {
		if !active[oldIndex] {
			continue
		}
		oldPathStart := record.PathStart
		oldReplacementStart := record.ReplacementStart
		copy(d.Paths[nextPath:nextPath+record.PathCount], d.Paths[oldPathStart:oldPathStart+record.PathCount])
		copy(d.Replacements[nextReplacement:nextReplacement+record.ReplacementCount], d.Replacements[oldReplacementStart:oldReplacementStart+record.ReplacementCount])
		record.PathStart = nextPath
		record.ReplacementStart = nextReplacement
		d.Journeys[nextJourney] = record
		remap[oldIndex] = journeyID(nextJourney)
		nextJourney++
		nextPath += record.PathCount
		nextReplacement += record.ReplacementCount
	}
	d.Journeys = d.Journeys[:nextJourney]
	d.Paths = d.Paths[:nextPath]
	d.Replacements = d.Replacements[:nextReplacement]

	for day, journeys := range d.DayJourneys {
		for index, journey := range journeys {
			journeys[index] = remap[journey]
		}
		d.DayJourneys[day] = journeys
	}
	for key, journeys := range d.Departures {
		if !keepDays[key.Day] {
			delete(d.Departures, key)
			continue
		}
		for index, entry := range journeys {
			if int(entry.journey()) >= len(remap) || !active[entry.journey()] {
				continue
			}
			remapped, ok := packDepartureEntry(remap[entry.journey()], entry.pathIndex(), entry.departureSeconds())
			if ok {
				journeys[index] = remapped
			}
		}
		d.Departures[key] = journeys
	}
	for key := range d.CompleteStops {
		if !keepDays[key.Day] {
			delete(d.CompleteStops, key)
		}
	}

	d.JourneyIDs = make(map[journeyKey]journeyID, len(d.Journeys))
	for index, journey := range d.Journeys {
		d.JourneyIDs[journeyKey{PrimaryID: journey.PrimaryID}] = journeyID(index)
	}
}

func rollingDates(now time.Time, behind int, ahead int) []time.Time {
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	dates := make([]time.Time, 0, behind+ahead+1)
	for offset := -behind; offset <= ahead; offset++ {
		dates = append(dates, start.AddDate(0, 0, offset))
	}
	return dates
}

func makeDayKey(value time.Time) dayKey {
	return dayKey(value.Year()*10000 + int(value.Month())*100 + value.Day())
}

func dayKeyDate(value dayKey, location *time.Location) time.Time {
	n := int(value)
	year := n / 10000
	month := time.Month((n / 100) % 100)
	day := n % 100
	return time.Date(year, month, day, 0, 0, 0, 0, location)
}

func serviceSeconds(value time.Time) int32 {
	if value.Year() > 1 {
		return int32(value.Hour()*3600 + value.Minute()*60 + value.Second())
	}
	start := time.Date(0, time.January, 1, 0, 0, 0, 0, value.Location())
	return int32(value.Sub(start) / time.Second)
}

func serviceTime(seconds int32) time.Time {
	return time.Date(0, time.January, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(seconds) * time.Second)
}

const (
	activityPickup uint8 = 1 << iota
	activitySetdown
	activityPass
)

func packActivities(values []ctdf.JourneyPathItemActivity) uint8 {
	var result uint8
	for _, value := range values {
		switch value {
		case ctdf.JourneyPathItemActivityPickup:
			result |= activityPickup
		case ctdf.JourneyPathItemActivitySetdown:
			result |= activitySetdown
		case ctdf.JourneyPathItemActivityPass:
			result |= activityPass
		}
	}
	return result
}

func unpackActivities(value uint8) []ctdf.JourneyPathItemActivity {
	result := make([]ctdf.JourneyPathItemActivity, 0, 3)
	if value&activityPickup != 0 {
		result = append(result, ctdf.JourneyPathItemActivityPickup)
	}
	if value&activitySetdown != 0 {
		result = append(result, ctdf.JourneyPathItemActivitySetdown)
	}
	if value&activityPass != 0 {
		result = append(result, ctdf.JourneyPathItemActivityPass)
	}
	return result
}

func (d *graphData) intern(value string) stringID {
	if id, exists := d.StringIDs[value]; exists {
		return id
	}
	id := stringID(len(d.Strings))
	d.Strings = append(d.Strings, value)
	d.StringIDs[value] = id
	return id
}

func (d *graphData) internStop(value string) stringID {
	id := d.intern(value)
	d.StopIDs[value] = id
	return id
}

// ensureBuildIndexesLocked restores the large lookup maps only when a sealed
// generation must accept a lazy fill or continue a later rolling build.
func (d *graphData) ensureBuildIndexesLocked() {
	if d.StringIDs == nil {
		d.StringIDs = make(map[string]stringID, len(d.Strings))
		for index, value := range d.Strings {
			d.StringIDs[value] = stringID(index)
		}
	}
	if d.JourneyIDs == nil {
		d.JourneyIDs = make(map[journeyKey]journeyID, len(d.Journeys))
		for index, journey := range d.Journeys {
			d.JourneyIDs[journeyKey{PrimaryID: journey.PrimaryID}] = journeyID(index)
		}
	}
	if d.JourneyDays == nil {
		d.JourneyDays = map[journeyDayKey]bool{}
	}
	if d.DayJourneys == nil {
		d.DayJourneys = map[dayKey][]journeyID{}
	}
}

// sealBuildIndexesLocked releases construction-only maps after a complete
// scan. StopIDs remains as the small request-facing lookup. JourneyDays only
// retains incomplete lazy-filled days, so completed rolling days do not keep a
// second membership map alongside the departure index.
func (d *graphData) sealBuildIndexesLocked() {
	for active := range d.JourneyDays {
		if d.CompleteDays[active.Day] {
			delete(d.JourneyDays, active)
		}
	}
	if len(d.JourneyDays) == 0 {
		d.JourneyDays = nil
	}
	d.StringIDs = nil
	d.JourneyIDs = nil
}

func (d *graphData) stringValue(id stringID) string {
	if int(id) >= len(d.Strings) {
		return ""
	}
	return d.Strings[id]
}

func (d *graphData) addJourneys(day dayKey, journeys []*ctdf.Journey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, journey := range journeys {
		d.addJourneyLocked(day, journey)
	}
}

func (d *graphData) addJourney(day dayKey, journey *ctdf.Journey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.addJourneyLocked(day, journey)
}

func (d *graphData) addJourneyLocked(day dayKey, journey *ctdf.Journey) {
	if journey == nil || journey.PrimaryIdentifier == "" || len(journey.Path) == 0 {
		return
	}
	d.ensureBuildIndexesLocked()
	primaryID := d.intern(journey.PrimaryIdentifier)
	key := journeyKey{PrimaryID: primaryID}
	id, exists := d.JourneyIDs[key]
	if !exists {
		record := journeyRecord{
			PrimaryID:          primaryID,
			ServiceRef:         d.intern(journey.ServiceRef),
			OperatorRef:        d.intern(journey.OperatorRef),
			DepartureTimezone:  d.intern(journey.DepartureTimezone),
			DestinationDisplay: d.intern(journey.DestinationDisplay),
			DepartureSeconds:   serviceSeconds(journey.DepartureTime),
			PathStart:          uint32(len(d.Paths)),
			PathCount:          uint32(len(journey.Path)),
			ReplacementStart:   uint32(len(d.Replacements)),
			ReplacementCount:   uint32(len(journey.ReplacesJourneyRefs)),
		}
		if journey.Path[0] != nil {
			record.InitialArrival = serviceSeconds(journey.Path[0].OriginArrivalTime)
		}
		if journey.DataSource != nil {
			record.DatasetID = d.intern(journey.DataSource.DatasetID)
		}
		if journey.OtherIdentifiers != nil {
			record.BlockNumber = d.intern(journey.OtherIdentifiers["BlockNumber"])
		}
		if journey.DetailedRailInformation != nil && journey.DetailedRailInformation.ReplacementBus {
			record.Flags |= 1
		}
		for _, replacement := range journey.ReplacesJourneyRefs {
			d.Replacements = append(d.Replacements, d.intern(replacement))
		}

		for _, path := range journey.Path {
			if path == nil {
				d.Paths = append(d.Paths, pathRecord{})
				continue
			}
			d.Paths = append(d.Paths, pathRecord{
				OriginStopRef:       d.internStop(path.OriginStopRef),
				DestinationStopRef:  d.internStop(path.DestinationStopRef),
				OriginPlatform:      d.intern(path.OriginPlatform),
				DestinationDisplay:  d.intern(path.DestinationDisplay),
				OriginDeparture:     serviceSeconds(path.OriginDepartureTime),
				DestinationArrival:  serviceSeconds(path.DestinationArrivalTime),
				OriginActivity:      packActivities(path.OriginActivity),
				DestinationActivity: packActivities(path.DestinationActivity),
			})
		}

		id = journeyID(len(d.Journeys))
		d.Journeys = append(d.Journeys, record)
		d.JourneyIDs[key] = id
	}

	activeKey := journeyDayKey{Day: day, Journey: id}
	if d.JourneyDays[activeKey] {
		return
	}
	d.JourneyDays[activeKey] = true
	d.DayJourneys[day] = append(d.DayJourneys[day], id)
	record := d.Journeys[id]
	for index := uint32(0); index < record.PathCount; index++ {
		path := d.Paths[record.PathStart+index]
		if d.stringValue(path.OriginStopRef) != "" && path.OriginActivity != activitySetdown {
			key := bucketKey{Day: day, StopRef: path.OriginStopRef}
			if entry, ok := packDepartureEntry(id, index, path.OriginDeparture); ok {
				d.Departures[key] = append(d.Departures[key], entry)
			} else {
				// The packed bounds are deliberately generous (65k calls and
				// roughly six days either side of midnight). Do not silently
				// create an unrouteable journey if corrupt input exceeds them.
				log.Warn().Str("journey", journey.PrimaryIdentifier).Uint32("path_index", index).Int32("departure_seconds", path.OriginDeparture).Msg("Journey omitted from departure index because its path or time exceeds packed bounds")
			}
		}
	}
}

func (d *graphData) stopComplete(day dayKey, canonical string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.CompleteDays[day] {
		return true
	}
	id, exists := d.StopIDs[canonical]
	return exists && d.CompleteStops[bucketKey{Day: day, StopRef: id}]
}

func (d *graphData) markStopComplete(day dayKey, canonical string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.ensureBuildIndexesLocked()
	d.CompleteStops[bucketKey{Day: day, StopRef: d.internStop(canonical)}] = true
}

type journeyCandidate struct {
	id               journeyID
	departureSeconds int32
}

func (d *graphData) materializeStop(day dayKey, stopRefs []string) []*ctdf.Journey {
	return d.materializeStopWindow(day, stopRefs, time.Time{}, 0)
}

func (d *graphData) materializeStopWindow(day dayKey, stopRefs []string, notBefore time.Time, limit int) []*ctdf.Journey {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stopIDs := make(map[stringID]struct{}, len(stopRefs))
	for _, stopRef := range stopRefs {
		if stopID, exists := d.StopIDs[stopRef]; exists {
			stopIDs[stopID] = struct{}{}
		}
	}
	threshold, hasThreshold := serviceWindowThreshold(dayKeyDate(day, notBefore.Location()), notBefore)
	candidates := make([]journeyCandidate, 0, 64)
	seen := map[journeyID]int{}
	for stopID := range stopIDs {
		entries := d.Departures[bucketKey{Day: day, StopRef: stopID}]
		start := 0
		if hasThreshold && d.CompleteDays[day] {
			start = sort.Search(len(entries), func(index int) bool { return entries[index].departureSeconds() >= threshold })
		}
		for _, entry := range entries[start:] {
			id := entry.journey()
			departureSeconds := entry.departureSeconds()
			if hasThreshold && departureSeconds < threshold {
				continue
			}
			if existing, exists := seen[id]; exists {
				if departureSeconds < candidates[existing].departureSeconds {
					candidates[existing].departureSeconds = departureSeconds
				}
				continue
			}
			seen[id] = len(candidates)
			candidates = append(candidates, journeyCandidate{
				id:               id,
				departureSeconds: departureSeconds,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].departureSeconds == candidates[j].departureSeconds {
			return candidates[i].id < candidates[j].id
		}
		return candidates[i].departureSeconds < candidates[j].departureSeconds
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	journeys := make([]*ctdf.Journey, 0, len(candidates))
	for _, candidate := range candidates {
		journeys = append(journeys, d.materializeJourney(candidate.id))
	}
	return journeys
}

type loadedJourneyCandidate struct {
	journey          *ctdf.Journey
	departureSeconds int32
}

func filterJourneyWindow(journeys []*ctdf.Journey, stopRefs []string, serviceDate time.Time, notBefore time.Time, limit int) []*ctdf.Journey {
	if len(journeys) == 0 {
		return journeys
	}
	requested := make(map[string]struct{}, len(stopRefs))
	for _, stopRef := range stopRefs {
		requested[stopRef] = struct{}{}
	}
	threshold, hasThreshold := serviceWindowThreshold(serviceDate, notBefore)
	candidates := make([]loadedJourneyCandidate, 0, len(journeys))
	for _, journey := range journeys {
		if journey == nil {
			continue
		}
		var earliest int32
		matched := false
		for _, path := range journey.Path {
			if path == nil {
				continue
			}
			if _, exists := requested[path.OriginStopRef]; !exists {
				continue
			}
			departureSeconds := serviceSeconds(path.OriginDepartureTime)
			if hasThreshold && departureSeconds < threshold {
				continue
			}
			if !matched || departureSeconds < earliest {
				earliest = departureSeconds
				matched = true
			}
		}
		if matched {
			candidates = append(candidates, loadedJourneyCandidate{
				journey:          journey,
				departureSeconds: earliest,
			})
		}
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].departureSeconds == candidates[j].departureSeconds {
			return candidates[i].journey.PrimaryIdentifier < candidates[j].journey.PrimaryIdentifier
		}
		return candidates[i].departureSeconds < candidates[j].departureSeconds
	})
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	filtered := make([]*ctdf.Journey, 0, len(candidates))
	for _, candidate := range candidates {
		filtered = append(filtered, candidate.journey)
	}
	return filtered
}

func serviceWindowThreshold(serviceDate time.Time, notBefore time.Time) (int32, bool) {
	if notBefore.IsZero() {
		return 0, false
	}
	location := notBefore.Location()
	start := time.Date(serviceDate.Year(), serviceDate.Month(), serviceDate.Day(), 0, 0, 0, 0, location)
	seconds := int64(notBefore.Sub(start) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	return int32(seconds), true
}

func (d *graphData) materializeJourney(id journeyID) *ctdf.Journey {
	if int(id) >= len(d.Journeys) {
		return nil
	}
	record := d.Journeys[id]
	journey := &ctdf.Journey{
		PrimaryIdentifier:  d.stringValue(record.PrimaryID),
		ServiceRef:         d.stringValue(record.ServiceRef),
		OperatorRef:        d.stringValue(record.OperatorRef),
		DepartureTimezone:  d.stringValue(record.DepartureTimezone),
		DestinationDisplay: d.stringValue(record.DestinationDisplay),
		DepartureTime:      serviceTime(record.DepartureSeconds),
		Availability: &ctdf.Availability{Match: []ctdf.AvailabilityRule{{
			Type:  ctdf.AvailabilityMatchAll,
			Value: "graph-materialized",
		}}},
		Path: make([]*ctdf.JourneyPathItem, 0, record.PathCount),
	}
	if blockNumber := d.stringValue(record.BlockNumber); blockNumber != "" {
		journey.OtherIdentifiers = map[string]string{"BlockNumber": blockNumber}
	}
	if datasetID := d.stringValue(record.DatasetID); datasetID != "" {
		journey.DataSource = &ctdf.DataSourceReference{DatasetID: datasetID}
	}
	if record.Flags&1 != 0 {
		journey.DetailedRailInformation = &ctdf.JourneyDetailedRail{ReplacementBus: true}
	}
	for index := uint32(0); index < record.ReplacementCount; index++ {
		journey.ReplacesJourneyRefs = append(journey.ReplacesJourneyRefs, d.stringValue(d.Replacements[record.ReplacementStart+index]))
	}
	for index := uint32(0); index < record.PathCount; index++ {
		path := d.Paths[record.PathStart+index]
		originArrival := record.InitialArrival
		if index > 0 {
			originArrival = d.Paths[record.PathStart+index-1].DestinationArrival
		}
		journey.Path = append(journey.Path, &ctdf.JourneyPathItem{
			OriginStopRef:          d.stringValue(path.OriginStopRef),
			DestinationStopRef:     d.stringValue(path.DestinationStopRef),
			OriginPlatform:         d.stringValue(path.OriginPlatform),
			DestinationDisplay:     d.stringValue(path.DestinationDisplay),
			OriginArrivalTime:      serviceTime(originArrival),
			OriginDepartureTime:    serviceTime(path.OriginDeparture),
			DestinationArrivalTime: serviceTime(path.DestinationArrival),
			OriginActivity:         unpackActivities(path.OriginActivity),
			DestinationActivity:    unpackActivities(path.DestinationActivity),
		})
	}
	return journey
}

type Stats struct {
	Strings            int
	Stops              int
	StopIdentifiers    int
	TransferEdges      int
	TopologyReady      bool
	StaticRideLinks    int
	StaticRoutingReady bool
	Journeys           int
	Paths              int
	DepartureBuckets   int
	ArrivalBuckets     int
	CompleteStops      int
	CompleteDays       int
	ServingToday       bool
	Lookups            LookupStats
	BackgroundBuild    BuildStats
	Snapshot           SnapshotStats
}

func (g *Graph) Stats() Stats {
	if g == nil {
		return Stats{}
	}
	stats := g.current.Load().stats()
	stats.Lookups = g.metrics.lookupStats()
	stats.BackgroundBuild = g.metrics.build.stats()
	stats.Snapshot = g.metrics.snapshot.stats()
	return stats
}

func (d *graphData) stats() Stats {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return Stats{
		Strings:            len(d.Strings),
		Stops:              len(d.Stops),
		StopIdentifiers:    len(d.StopIdentifiers),
		TransferEdges:      len(d.Transfers),
		TopologyReady:      d.TopologyReady,
		StaticRideLinks:    len(d.ArrivalPatterns),
		StaticRoutingReady: d.StaticRoutingReady,
		Journeys:           len(d.Journeys),
		Paths:              len(d.Paths),
		DepartureBuckets:   len(d.Departures),
		ArrivalBuckets:     0,
		CompleteStops:      len(d.CompleteStops),
		CompleteDays:       len(d.CompleteDays),
		ServingToday:       d.CompleteDays[makeDayKey(time.Now())],
	}
}

func envBool(env map[string]string, key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(env[key]))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envInt(env map[string]string, key string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(env[key]))
	if err != nil {
		return fallback
	}
	return value
}

func envDuration(env map[string]string, key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(env[key])
	if value == "" {
		return fallback
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return duration
}
