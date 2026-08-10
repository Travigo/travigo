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

const snapshotVersion = 2

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

type journeyRecord struct {
	PrimaryID          stringID
	ServiceRef         stringID
	OperatorRef        stringID
	DepartureTimezone  stringID
	DestinationDisplay stringID
	DatasetID          stringID
	BlockNumber        stringID
	DepartureSeconds   int32
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
	DestinationPlatform stringID
	DestinationDisplay  stringID
	OriginArrival       int32
	OriginDeparture     int32
	DestinationArrival  int32
	OriginActivity      uint8
	DestinationActivity uint8
}

type graphData struct {
	mu sync.RWMutex

	Strings       []string
	StringIDs     map[string]stringID
	Journeys      []journeyRecord
	Paths         []pathRecord
	Replacements  []stringID
	JourneyIDs    map[journeyKey]journeyID
	JourneyDays   map[journeyDayKey]bool
	Departures    map[bucketKey][]journeyID
	CompleteStops map[bucketKey]bool
	CompleteDays  map[dayKey]bool
	ScanDays      []dayKey
	ScanCursor    string
	ScanProcessed int64
	ScanActive    int64
}

type Loader interface {
	LoadStopJourneys(ctx context.Context, stopRefs []string, serviceDate time.Time) ([]*ctdf.Journey, error)
	ScanJourneys(ctx context.Context, after string, visit func(*ctdf.Journey, string) error) error
}

type JourneyCounter interface {
	JourneyCount(ctx context.Context) (int64, error)
}

// Provider is the query boundary used by departure-board consumers. Graph
// implements it in the graph service and Client implements it in web-api.
type Provider interface {
	JourneysForStop(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time) ([]*ctdf.Journey, error)
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
	metrics graphMetrics

	snapshotMu sync.Mutex
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
		JourneyIDs:    map[journeyKey]journeyID{},
		JourneyDays:   map[journeyDayKey]bool{},
		Departures:    map[bucketKey][]journeyID{},
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
			restored = g.Stats().Journeys > 0
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

func (g *Graph) JourneysForStop(ctx context.Context, stop *ctdf.Stop, serviceDate time.Time) ([]*ctdf.Journey, error) {
	if g == nil || g.loader == nil || stop == nil {
		return nil, fmt.Errorf("departure graph is not configured")
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
	if !complete {
		fillKey := strconv.Itoa(int(day)) + "\x00" + canonical
		_, err, _ := g.fills.Do(fillKey, func() (any, error) {
			active := g.current.Load()
			if active.stopComplete(day, canonical) {
				return nil, nil
			}
			started := g.metrics.beginLazyFill()
			var fillErr error
			defer func() { g.metrics.finishLazyFill(started, fillErr) }()
			journeys, fillErr := g.loader.LoadStopJourneys(ctx, stopRefs, serviceDate)
			if fillErr != nil {
				return nil, fillErr
			}
			active.addJourneys(day, journeys)
			active.markStopComplete(day, canonical)
			log.Debug().
				Str("stop", canonical).
				Str("service_date", serviceDate.Format("2006-01-02")).
				Int("journeys", len(journeys)).
				Msg("Departure graph filled requested stop")
			return nil, nil
		})
		if err != nil {
			return nil, err
		}
		data = g.current.Load()
	}

	return data.materializeStop(day, stopRefs), nil
}

func (g *Graph) rebuildRolling(ctx context.Context, now time.Time) (err error) {
	dates := rollingDates(now, g.config.DaysBehind, g.config.DaysAhead)
	next := g.current.Load()
	if next.coversRolling(now, g.config.DaysBehind, g.config.DaysAhead) {
		next = newGraphData()
		// Refresh in place from an empty generation so peak memory does not
		// contain two multi-gigabyte graphs. Requests remain available: a miss
		// fills its stop into this generation while the scan continues.
		g.current.Store(next)
	}
	configured, matching, cursor, processed, active := next.scanState(dates)
	if configured && !matching {
		next = newGraphData()
		g.current.Store(next)
		cursor, processed, active = "", 0, 0
	}
	if !configured || !matching {
		next.setScanProgress(dates, cursor, processed, active)
	}
	g.metrics.build.begin(processed, active)
	defer func() { g.metrics.build.finish(err) }()
	if counter, ok := g.loader.(JourneyCounter); ok {
		if total, countErr := counter.JourneyCount(ctx); countErr == nil {
			g.metrics.build.setEstimatedJourneys(total)
		}
	}
	processedThisRun := int64(0)
	latestCursor := cursor
	started := time.Now()

	err = g.loader.ScanJourneys(ctx, cursor, func(journey *ctdf.Journey, journeyCursor string) error {
		processed++
		processedThisRun++
		activeForJourney := int64(0)
		if journey != nil && journey.Availability != nil {
			for _, serviceDate := range dates {
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
	next.completeScan(dates)

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
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, serviceDate := range dates {
		d.CompleteDays[makeDayKey(serviceDate)] = true
	}
	d.ScanDays = nil
	d.ScanCursor = ""
	d.ScanProcessed = 0
	d.ScanActive = 0
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
				OriginStopRef:       d.intern(path.OriginStopRef),
				DestinationStopRef:  d.intern(path.DestinationStopRef),
				OriginPlatform:      d.intern(path.OriginPlatform),
				DestinationPlatform: d.intern(path.DestinationPlatform),
				DestinationDisplay:  d.intern(path.DestinationDisplay),
				OriginArrival:       serviceSeconds(path.OriginArrivalTime),
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
	record := d.Journeys[id]
	for index := uint32(0); index < record.PathCount; index++ {
		originRef := d.Paths[record.PathStart+index].OriginStopRef
		if d.stringValue(originRef) == "" {
			continue
		}
		d.Departures[bucketKey{Day: day, StopRef: originRef}] = append(d.Departures[bucketKey{Day: day, StopRef: originRef}], id)
	}
}

func (d *graphData) stopComplete(day dayKey, canonical string) bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.CompleteDays[day] {
		return true
	}
	id, exists := d.StringIDs[canonical]
	return exists && d.CompleteStops[bucketKey{Day: day, StopRef: id}]
}

func (d *graphData) markStopComplete(day dayKey, canonical string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.CompleteStops[bucketKey{Day: day, StopRef: d.intern(canonical)}] = true
}

func (d *graphData) materializeStop(day dayKey, stopRefs []string) []*ctdf.Journey {
	d.mu.RLock()
	defer d.mu.RUnlock()

	ids := make([]journeyID, 0, 64)
	seen := map[journeyID]struct{}{}
	for _, stopRef := range stopRefs {
		stopID, exists := d.StringIDs[stopRef]
		if !exists {
			continue
		}
		for _, id := range d.Departures[bucketKey{Day: day, StopRef: stopID}] {
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		return d.Journeys[ids[i]].DepartureSeconds < d.Journeys[ids[j]].DepartureSeconds
	})
	journeys := make([]*ctdf.Journey, 0, len(ids))
	for _, id := range ids {
		journeys = append(journeys, d.materializeJourney(id))
	}
	return journeys
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
		journey.Path = append(journey.Path, &ctdf.JourneyPathItem{
			OriginStopRef:          d.stringValue(path.OriginStopRef),
			DestinationStopRef:     d.stringValue(path.DestinationStopRef),
			OriginPlatform:         d.stringValue(path.OriginPlatform),
			DestinationPlatform:    d.stringValue(path.DestinationPlatform),
			DestinationDisplay:     d.stringValue(path.DestinationDisplay),
			OriginArrivalTime:      serviceTime(path.OriginArrival),
			OriginDepartureTime:    serviceTime(path.OriginDeparture),
			DestinationArrivalTime: serviceTime(path.DestinationArrival),
			OriginActivity:         unpackActivities(path.OriginActivity),
			DestinationActivity:    unpackActivities(path.DestinationActivity),
		})
	}
	return journey
}

type Stats struct {
	Strings          int
	Journeys         int
	Paths            int
	DepartureBuckets int
	CompleteStops    int
	CompleteDays     int
	Lookups          LookupStats
	BackgroundBuild  BuildStats
	Snapshot         SnapshotStats
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
		Strings:          len(d.Strings),
		Journeys:         len(d.Journeys),
		Paths:            len(d.Paths),
		DepartureBuckets: len(d.Departures),
		CompleteStops:    len(d.CompleteStops),
		CompleteDays:     len(d.CompleteDays),
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
