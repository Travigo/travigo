package ctdf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"runtime"
	"sort"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type DepartureBoard struct {
	OccurrenceIdentifier string                   `groups:"basic,web-board"`
	ServiceDate          string                   `groups:"basic,web-board"`
	DisplayReference     *BoardDisplayReference   `groups:"internal" json:"-" bson:"-"`
	Journey              *Journey                 `groups:"basic,departures-llm,web-board"`
	DestinationDisplay   string                   `groups:"basic,departures-llm,web-board"`
	Type                 DepartureBoardRecordType `groups:"basic,departures-llm,web-board"`
	Delayed              bool                     `groups:"basic,departures-llm,web-board"`

	Platform     string `groups:"basic,departures-llm,web-board"`
	PlatformType string `groups:"basic,departures-llm,web-board"`

	Time time.Time `groups:"basic,departures-llm,web-board"`
}

// BoardType identifies whether a board lists vehicles leaving or arriving at a
// stop. An empty value remains a departure board for backwards compatibility.
type BoardType string

const (
	BoardTypeDeparture BoardType = "departure"
	BoardTypeArrival   BoardType = "arrival"
)

func (t BoardType) IsArrival() bool {
	return t == BoardTypeArrival
}

func boardStopIndexOffset(boardType BoardType) int {
	if boardType.IsArrival() {
		return 1
	}
	return 0
}

type DepartureBoardRecordType string

const (
	DepartureBoardRecordTypeScheduled       DepartureBoardRecordType = "Scheduled"
	DepartureBoardRecordTypeRealtimeTracked DepartureBoardRecordType = "RealtimeTracked"
	DepartureBoardRecordTypeEstimated       DepartureBoardRecordType = "Estimated"
	DepartureBoardRecordTypeCancelled       DepartureBoardRecordType = "Cancelled"
)

// DepartureBoardRealtimeLookup keeps realtime reads outside ctdf. realtimestore
// imports ctdf, so ctdf cannot import realtimestore without creating a cycle.
type DepartureBoardRealtimeLookup struct {
	ByJourneyID         map[string]*RealtimeJourney
	CancelledJourneyIDs map[string]struct{}
	StopAliases         map[string][]string
	FindByJourneyIDs    func(journeyRefs []string) map[string]*RealtimeJourney
}

type blockJourneyReference struct {
	PrimaryIdentifier string
	DepartureTime     time.Time
}

type blockEstimateCandidate struct {
	entry    *DepartureBoard
	blockKey string
	refs     []string
}

type blockEstimateStats struct {
	candidates      int64
	blockJourneys   int64
	realtimeMatched int64
	estimated       int64
}

// precedingBlockJourneyRefs returns the block journeys that run before target,
// newest first. A realtime journey for one of these is the vehicle that can
// carry delay into target; a later journey must not influence it.
func precedingBlockJourneyRefs(blockJourneys []blockJourneyReference, target *Journey) []string {
	if target == nil {
		return nil
	}
	refs := make([]string, 0, len(blockJourneys))
	for index := len(blockJourneys) - 1; index >= 0; index-- {
		blockJourney := blockJourneys[index]
		if blockJourney.PrimaryIdentifier == "" || !blockJourney.DepartureTime.Before(target.DepartureTime) {
			continue
		}
		refs = append(refs, blockJourney.PrimaryIdentifier)
	}
	return refs
}

// IsBoardJourneyCancelled applies cancellation signals that are independent of
// whether a realtime stop update exists for the requested board stop.
func IsBoardJourneyCancelled(journey *Journey, realtimeJourney *RealtimeJourney, cancelledJourneyIDs map[string]struct{}) bool {
	if realtimeJourney != nil && realtimeJourney.Cancelled {
		return true
	}
	if journey == nil {
		return false
	}
	_, cancelledByAlert := cancelledJourneyIDs[journey.PrimaryIdentifier]
	return cancelledByAlert
}

// DeduplicateBoardEntries keeps the first board record for each journey. This
// lets a realtime record take precedence over a scheduled backfill record.
func DeduplicateBoardEntries(entries []*DepartureBoard) []*DepartureBoard {
	seenJourneyIDs := make(map[string]struct{}, len(entries))
	deduplicated := make([]*DepartureBoard, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.Journey == nil || entry.Journey.PrimaryIdentifier == "" {
			deduplicated = append(deduplicated, entry)
			continue
		}
		journeyID := entry.OccurrenceIdentifier
		if journeyID == "" {
			journeyID = entry.Journey.PrimaryIdentifier
		}
		if _, seen := seenJourneyIDs[journeyID]; seen {
			continue
		}
		seenJourneyIDs[journeyID] = struct{}{}
		deduplicated = append(deduplicated, entry)
	}
	return deduplicated
}

func BoardOccurrenceIdentifier(journeyID, serviceDate string, stopIndex int, boardType BoardType) string {
	if journeyID == "" {
		return ""
	}
	encoded, _ := json.Marshal([]any{journeyID, serviceDate, stopIndex, boardType})
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func boardPathStopRef(path *JourneyPathItem, boardType BoardType) string {
	if boardType.IsArrival() {
		return path.DestinationStopRef
	}
	return path.OriginStopRef
}

func boardPathTime(path *JourneyPathItem, boardType BoardType) time.Time {
	if boardType.IsArrival() {
		return path.DestinationArrivalTime
	}
	return path.OriginDepartureTime
}

func boardPathPlatform(path *JourneyPathItem, boardType BoardType) string {
	if boardType.IsArrival() {
		return path.DestinationPlatform
	}
	return path.OriginPlatform
}

func boardPathIsUnavailable(path *JourneyPathItem, boardType BoardType) bool {
	activity := path.OriginActivity
	var unavailableActivity JourneyPathItemActivity = JourneyPathItemActivitySetdown
	if boardType.IsArrival() {
		activity = path.DestinationActivity
		unavailableActivity = JourneyPathItemActivityPickup
	}
	return len(activity) == 1 && activity[0] == unavailableActivity
}

func boardRealtimeStopTime(stop *RealtimeJourneyStops, boardType BoardType) time.Time {
	if boardType.IsArrival() {
		return stop.ArrivalTime
	}
	return stop.DepartureTime
}

func boardRealtimeStop(journey *RealtimeJourney, stopRef string, stopIndex int, aliases map[string][]string) *RealtimeJourneyStops {
	if stop := journey.RealtimeStop(stopRef, stopIndex); stop != nil {
		return stop
	}
	for _, alias := range aliases[stopRef] {
		if stop := journey.RealtimeStop(alias, stopIndex); stop != nil {
			return stop
		}
	}
	return nil
}

func boardEntryIsDelayed(scheduledTime, realtimeTime time.Time, cancelled bool) bool {
	return !cancelled && realtimeTime.After(scheduledTime)
}

// Realtime run dates are service-date labels, not instants in the stop zone.
func boardRealtimeCancelsServiceDate(realtime *RealtimeJourney, serviceDate time.Time) bool {
	if realtime == nil || !realtime.Cancelled {
		return false
	}
	return boardRealtimeMatchesServiceDate(realtime, serviceDate)
}

func boardRealtimeMatchesServiceDate(realtime *RealtimeJourney, serviceDate time.Time) bool {
	return realtime != nil && !realtime.JourneyRunDate.IsZero() &&
		realtime.JourneyRunDate.Format(YearMonthDayFormat) == serviceDate.Format(YearMonthDayFormat)
}

// boardRealtimeTimeOnDate resolves the two forms used by realtime producers:
// date-less clock values (year 0/1) and absolute timestamps. Missing realtime
// times fall back to the schedule rather than becoming a midnight departure.
func boardRealtimeTimeOnDate(scheduledTime, realtimeTime time.Time) time.Time {
	if realtimeTime.IsZero() {
		return scheduledTime
	}

	if realtimeTime.Year() > 1 {
		return realtimeTime.In(scheduledTime.Location())
	}

	candidate := time.Date(
		scheduledTime.Year(), scheduledTime.Month(), scheduledTime.Day(),
		realtimeTime.Hour(), realtimeTime.Minute(), realtimeTime.Second(), realtimeTime.Nanosecond(),
		scheduledTime.Location(),
	)
	closest := candidate
	for _, adjacent := range []time.Time{candidate.AddDate(0, 0, -1), candidate.AddDate(0, 0, 1)} {
		if absoluteDuration(adjacent.Sub(scheduledTime)) < absoluteDuration(closest.Sub(scheduledTime)) {
			closest = adjacent
		}
	}

	return closest
}

func absoluteDuration(duration time.Duration) time.Duration {
	if duration < 0 {
		return -duration
	}
	return duration
}

// serviceTimeOnDate converts a GTFS service-day time into a real date without
// discarding the service-day overflow represented by times such as 25:30:00.
func serviceTimeOnDate(dateTime time.Time, serviceTime time.Time) time.Time {
	// GTFS service clocks use noon minus 12 elapsed hours as their day origin.
	serviceDayStart := time.Date(dateTime.Year(), dateTime.Month(), dateTime.Day(), 12, 0, 0, 0, dateTime.Location()).Add(-12 * time.Hour)
	encodedStart := time.Date(0, time.January, 1, 0, 0, 0, 0, serviceTime.Location())
	return serviceDayStart.Add(serviceTime.Sub(encodedStart))
}

func boardClockTimeOnServiceDate(journey *Journey, serviceDate, clock time.Time) time.Time {
	if clock.IsZero() {
		return time.Time{}
	}
	if clock.Year() > 1 {
		return clock.In(serviceDate.Location())
	}
	if journey != nil && journey.DataSource != nil && journey.DataSource.OriginalFormat == "gtfs-schedule" {
		return serviceTimeOnDate(serviceDate, clock)
	}

	// CIF-style schedules provide local wall-clock values. Preserve that local
	// clock on the service date; adding an elapsed duration from the GTFS
	// service-day origin would shift calls across a DST transition.
	encodedStart := time.Date(0, time.January, 1, 0, 0, 0, 0, clock.Location())
	dayOffset := int(clock.Sub(encodedStart) / (24 * time.Hour))
	localDate := serviceDate.AddDate(0, 0, dayOffset)
	return time.Date(localDate.Year(), localDate.Month(), localDate.Day(), clock.Hour(), clock.Minute(), clock.Second(), clock.Nanosecond(), serviceDate.Location())
}

// boardScheduledTimeOnServiceDate resolves path times that are stored as
// clock times and may wrap at midnight even when the feed did not encode the
// following day as an hour value above 24. Keep the path's clock sequence
// monotonic so an after-midnight call belongs to the date the train started.
func boardScheduledTimeOnServiceDate(journey *Journey, pathIndex int, serviceDate time.Time, boardType BoardType) time.Time {
	if journey == nil || pathIndex < 0 || pathIndex >= len(journey.Path) {
		return time.Time{}
	}

	target := time.Time{}
	previous := time.Time{}
	for index, path := range journey.Path[:pathIndex+1] {
		if path == nil {
			continue
		}
		for _, event := range []struct {
			clock  time.Time
			target bool
		}{
			{clock: path.OriginArrivalTime},
			{clock: path.OriginDepartureTime, target: index == pathIndex && !boardType.IsArrival()},
			{clock: path.DestinationArrivalTime, target: index == pathIndex && boardType.IsArrival()},
		} {
			if event.clock.IsZero() {
				continue
			}
			at := boardClockTimeOnServiceDate(journey, serviceDate, event.clock)
			for !previous.IsZero() && at.Before(previous) {
				if event.clock.Year() > 1 {
					break
				}
				if journey != nil && journey.DataSource != nil && journey.DataSource.OriginalFormat == "gtfs-schedule" {
					at = at.Add(24 * time.Hour)
				} else {
					at = time.Date(at.Year(), at.Month(), at.Day()+1, event.clock.Hour(), event.clock.Minute(), event.clock.Second(), event.clock.Nanosecond(), at.Location())
				}
			}
			previous = at
			if event.target {
				target = at
			}
		}
	}
	if !target.IsZero() {
		return target
	}
	return boardClockTimeOnServiceDate(journey, serviceDate, boardPathTime(journey.Path[pathIndex], boardType))
}

// BoardDestinationDisplay returns the service destination for departures and
// the journey origin for arrivals. It never loads missing stop references;
// selected API entries resolve those references in one batch.
func BoardDestinationDisplay(journey *Journey, fallback string, boardType BoardType) string {
	if !boardType.IsArrival() || journey == nil || len(journey.Path) == 0 {
		return fallback
	}

	firstPathItem := journey.Path[0]
	if firstPathItem == nil {
		return fallback
	}

	if firstPathItem.OriginStop != nil && firstPathItem.OriginStop.PrimaryName != "" {
		return firstPathItem.OriginStop.PrimaryName
	}

	return firstPathItem.OriginStopRef
}

// BoardDestinationDisplayWithRealtime shortens a departure's destination when
// realtime marks the scheduled terminal call as cancelled. Whole-journey
// cancellations retain the scheduled destination.
func BoardDestinationDisplayWithRealtime(journey *Journey, realtimeJourney *RealtimeJourney, fallback string, boardType BoardType, wholeJourneyCancelled bool) string {
	display := BoardDestinationDisplay(journey, fallback, boardType)
	if boardType.IsArrival() || wholeJourneyCancelled || journey == nil || realtimeJourney == nil || len(journey.Path) == 0 {
		return display
	}

	terminalIndex := len(journey.Path)
	terminalPathItem := journey.Path[terminalIndex-1]
	if terminalPathItem == nil {
		return display
	}
	terminalStop := realtimeJourney.RealtimeStop(terminalPathItem.DestinationStopRef, terminalIndex)
	if terminalStop == nil || !terminalStop.Cancelled {
		return display
	}

	for journeyStopIndex := terminalIndex - 1; journeyStopIndex >= 0; journeyStopIndex-- {
		var stopRef string
		var pathItem *JourneyPathItem
		if journeyStopIndex == 0 {
			pathItem = journey.Path[0]
			if pathItem == nil {
				continue
			}
			stopRef = pathItem.OriginStopRef
		} else {
			pathItem = journey.Path[journeyStopIndex-1]
			if pathItem == nil {
				continue
			}
			stopRef = pathItem.DestinationStopRef
		}

		realtimeStop := realtimeJourney.RealtimeStop(stopRef, journeyStopIndex)
		if realtimeStop != nil && realtimeStop.Cancelled {
			continue
		}

		var stop *Stop
		if journeyStopIndex == 0 {
			stop = pathItem.OriginStop
		} else {
			stop = pathItem.DestinationStop
		}
		if stop == nil {
			return display
		}
		if journey.Service != nil {
			for _, identifier := range stop.GetAllStopIDs() {
				if override := journey.Service.StopNameOverrides[identifier]; override != "" {
					return override
				}
			}
		}
		if stop.PrimaryName != "" {
			return stop.PrimaryName
		}
		return display
	}

	return display
}

// GenerateDepartureBoardFromJourneys is retained for callers that explicitly
// need a departure board. New code should use GenerateBoardFromJourneys.
func GenerateDepartureBoardFromJourneys(journeys []*Journey, stopRefs []string, dateTime time.Time, doEstimates bool, realtimeLookup *DepartureBoardRealtimeLookup) []*DepartureBoard {
	return GenerateBoardFromJourneys(journeys, stopRefs, dateTime, doEstimates, realtimeLookup, BoardTypeDeparture)
}

// GenerateBoardFromJourneys creates either an arrival or departure board from
// the same scheduled journeys. The selected path endpoint controls the stop,
// activity, platform, and scheduled/realtime time used for each record.
func GenerateBoardFromJourneys(journeys []*Journey, stopRefs []string, dateTime time.Time, doEstimates bool, realtimeLookup *DepartureBoardRealtimeLookup, boardType BoardType) []*DepartureBoard {
	return GenerateBoardFromJourneysForServiceDate(journeys, stopRefs, dateTime, dateTime, doEstimates, realtimeLookup, boardType)
}

// GenerateBoardFromJourneysForServiceDate separates the operating date from
// the requested lower bound, allowing calls after midnight on an earlier run.
func GenerateBoardFromJourneysForServiceDate(journeys []*Journey, stopRefs []string, dateTime, startTime time.Time, doEstimates bool, realtimeLookup *DepartureBoardRealtimeLookup, boardType BoardType) []*DepartureBoard {
	generationStart := time.Now()
	inputJourneyCount := len(journeys)

	// Performance Review [Set G] [PR010] [P1] (boards): Deduplication chooses the first timetable
	// hash before realtime/cancellation lookup. Two equivalent journeys with distinct
	// IDs lose the second one's live update or cancellation if only that ID is mapped.
	// Select a deterministic representative after resolving run-specific realtime
	// identity; also preserve differences in pickup/setdown permissions and platforms,
	// which GenerateFunctionalHash currently omits.
	journeys = FilterIdenticalJourneys(journeys, true)
	uniqueJourneyCount := len(journeys)
	if realtimeLookup == nil {
		realtimeLookup = &DepartureBoardRealtimeLookup{}
	}

	var availabilityMatchedCount atomic.Int64
	var tooOldSkippedCount atomic.Int64
	var prefetchedRealtimeAppliedCount atomic.Int64
	var replacementSuppressedCount atomic.Int64
	var stopMatchedCount atomic.Int64
	var activitySkippedCount atomic.Int64
	var realtimeStopMatchedCount atomic.Int64
	var cancelledCount atomic.Int64
	var beforeStartSkippedCount atomic.Int64
	var replacementBusCount atomic.Int64

	stopRefsSet := make(map[string]struct{}, len(stopRefs))
	for _, stopRef := range stopRefs {
		stopRefsSet[stopRef] = struct{}{}
	}
	// Performance Review [Set K] [PR011] [P2] (boards): Every board generation creates its own
	// pool of up to GOMAXPROCS workers and updates shared atomic counters per candidate.
	// Benchmark sequential/chunked generation for short paths and a shared concurrency
	// budget under simultaneous polling; per-request worker limits do not bound total
	// database reads or CPU work, and all candidates are retained until Wait completes.
	p := pool.NewWithResults[[]*DepartureBoard]()
	maxGoroutines := runtime.GOMAXPROCS(0)
	if len(journeys) < maxGoroutines {
		maxGoroutines = len(journeys)
	}
	if maxGoroutines < 1 {
		maxGoroutines = 1
	}
	p.WithMaxGoroutines(maxGoroutines)

	for _, journey := range journeys {
		p.Go(func() []*DepartureBoard {
			var occurrences []*DepartureBoard
			for occurrenceIndex, occurrencePath := range journey.Path {
				if occurrencePath == nil {
					continue
				}
				if _, ok := stopRefsSet[boardPathStopRef(occurrencePath, boardType)]; !ok {
					continue
				}
				entry := func() *DepartureBoard {
					var stopTime time.Time
					var scheduledStopTime time.Time
					var realtimeStopTime time.Time
					var stopPlatform string
					var stopPlatformType string
					var destinationDisplay string
					var displayReference *BoardDisplayReference
					var delayed bool
					departureBoardRecordType := DepartureBoardRecordTypeScheduled

					if journey.Availability.MatchDate(dateTime) {
						availabilityMatchedCount.Add(1)
						scheduledStopTime = boardScheduledTimeOnServiceDate(journey, occurrenceIndex, dateTime, boardType)
						if startTime.Sub(scheduledStopTime) > 4*time.Hour {
							tooOldSkippedCount.Add(1)
							return nil
						}

						prefetchedRealtimeJourney := realtimeLookup.ByJourneyID[journey.PrimaryIdentifier]
						boardRealtime := journey.RealtimeJourney
						if prefetchedRealtimeJourney != nil && len(prefetchedRealtimeJourney.SuppressFromDepartureDates) > 0 && prefetchedRealtimeJourney.SuppressesBoardAt(dateTime) {
							replacementSuppressedCount.Add(1)
							return nil
						}
						if !boardRealtimeMatchesServiceDate(journey.RealtimeJourney, dateTime) {
							journey.RealtimeJourney = nil
						}
						if boardRealtimeMatchesServiceDate(prefetchedRealtimeJourney, dateTime) && (prefetchedRealtimeJourney.Cancelled || prefetchedRealtimeJourney.SuppressFromDepartures || prefetchedRealtimeJourney.IsActive()) {
							journey.RealtimeJourney = prefetchedRealtimeJourney
							boardRealtime = prefetchedRealtimeJourney
							prefetchedRealtimeAppliedCount.Add(1)
						}
						if journey.RealtimeJourney != nil && journey.RealtimeJourney.SuppressesBoardAt(dateTime) {
							replacementSuppressedCount.Add(1)
							return nil
						}

						stopMatchedCount.Add(1)
						stopPlatform = boardPathPlatform(occurrencePath, boardType)
						stopPlatformType = "ESTIMATED"

						// A departure needs pickup permission; an arrival needs setdown permission.
						if boardPathIsUnavailable(occurrencePath, boardType) {
							activitySkippedCount.Add(1)
							return nil
						}

						// Use the realtime estimated stop time based if realtime is available
						if journey.RealtimeJourney != nil {
							var realtimeJourneyStop *RealtimeJourneyStops

							journeyStopIndex := occurrenceIndex + boardStopIndexOffset(boardType)
							realtimeJourneyStop = boardRealtimeStop(journey.RealtimeJourney, boardPathStopRef(occurrencePath, boardType), journeyStopIndex, realtimeLookup.StopAliases)

							if realtimeJourneyStop != nil {
								realtimeStopMatchedCount.Add(1)
								if realtimeJourneyStop.Cancelled {
									departureBoardRecordType = DepartureBoardRecordTypeCancelled
								}

								if journey.RealtimeJourney.ActivelyTracked {
									realtimeStopTime = boardRealtimeStopTime(realtimeJourneyStop, boardType)
								}

								if realtimeJourneyStop.Platform != "" {
									stopPlatform = realtimeJourneyStop.Platform
									stopPlatformType = "ACTUAL"
								}
							}

							if journey.RealtimeJourney.ActivelyTracked && departureBoardRecordType != DepartureBoardRecordTypeCancelled {
								departureBoardRecordType = DepartureBoardRecordTypeRealtimeTracked
							}
						}

						wholeJourneyCancelled := boardRealtimeCancelsServiceDate(boardRealtime, dateTime) ||
							boardRealtimeCancelsServiceDate(prefetchedRealtimeJourney, dateTime) ||
							IsBoardJourneyCancelled(journey, nil, realtimeLookup.CancelledJourneyIDs)
						if wholeJourneyCancelled {
							departureBoardRecordType = DepartureBoardRecordTypeCancelled
						}
						if departureBoardRecordType == DepartureBoardRecordTypeCancelled {
							cancelledCount.Add(1)
						}

						stopTime = scheduledStopTime
						if journey.RealtimeJourney != nil && journey.RealtimeJourney.ActivelyTracked {
							stopTime = boardRealtimeTimeOnDate(stopTime, realtimeStopTime)
						}
						delayed = boardEntryIsDelayed(scheduledStopTime, stopTime, departureBoardRecordType == DepartureBoardRecordTypeCancelled)

						destinationDisplay = BoardDestinationDisplayWithRealtime(journey, journey.RealtimeJourney, occurrencePath.DestinationDisplay, boardType, wholeJourneyCancelled)
						displayReference = BoardDisplayReferenceFor(journey, journey.RealtimeJourney, boardType, wholeJourneyCancelled)

						if stopTime.Before(startTime) {
							beforeStartSkippedCount.Add(1)
							return nil
						}

						if journey.DetailedRailInformation != nil && journey.DetailedRailInformation.ReplacementBus {
							stopPlatform = "BUS"
							stopPlatformType = "ACTUAL"
							replacementBusCount.Add(1)
						}

						return &DepartureBoard{
							OccurrenceIdentifier: BoardOccurrenceIdentifier(journey.PrimaryIdentifier, dateTime.Format(YearMonthDayFormat), occurrenceIndex+boardStopIndexOffset(boardType), boardType),
							ServiceDate:          dateTime.Format(YearMonthDayFormat),
							Journey:              journey,
							Time:                 stopTime,
							DestinationDisplay:   destinationDisplay,
							DisplayReference:     displayReference,
							Type:                 departureBoardRecordType,
							Delayed:              delayed,
							Platform:             stopPlatform,
							PlatformType:         stopPlatformType,
						}
					}

					return nil
				}()
				if entry != nil {
					occurrences = append(occurrences, entry)
				}
			}
			return occurrences
		})
	}

	departureBoardWithNil := p.Wait()
	departureBoard := make([]*DepartureBoard, 0, len(departureBoardWithNil))

	for _, entries := range departureBoardWithNil {
		departureBoard = append(departureBoard, entries...)
	}
	departureBoard = DeduplicateBoardEntries(departureBoard)
	estimateStats := applyBlockEstimates(departureBoard, startTime, doEstimates, realtimeLookup)

	log.Debug().
		Int("input_journeys", inputJourneyCount).
		Int("unique_journeys", uniqueJourneyCount).
		Int("stop_refs", len(stopRefs)).
		Int("max_goroutines", maxGoroutines).
		Bool("do_estimates", doEstimates).
		Time("date", dateTime).
		Int64("availability_matched", availabilityMatchedCount.Load()).
		Int64("too_old_skipped", tooOldSkippedCount.Load()).
		Int64("prefetched_realtime_applied", prefetchedRealtimeAppliedCount.Load()).
		Int64("replacement_suppressed", replacementSuppressedCount.Load()).
		Int64("stop_matched", stopMatchedCount.Load()).
		Int64("activity_skipped", activitySkippedCount.Load()).
		Int64("realtime_stop_matched", realtimeStopMatchedCount.Load()).
		Int64("cancelled_records", cancelledCount.Load()).
		Int64("before_start_skipped", beforeStartSkippedCount.Load()).
		Int64("replacement_bus_records", replacementBusCount.Load()).
		Int64("estimate_candidates", estimateStats.candidates).
		Int64("estimate_block_journeys", estimateStats.blockJourneys).
		Int64("estimate_realtime_matched", estimateStats.realtimeMatched).
		Int64("estimated_records", estimateStats.estimated).
		Int("journey_results", len(departureBoardWithNil)).
		Str("board_type", string(boardType)).
		Int("generated_entries", len(departureBoard)).
		Dur("duration", time.Since(generationStart)).
		Msg("Departure board generation stats")

	return departureBoard
}

func journeyBlockKey(journey *Journey) string {
	if journey == nil || journey.OtherIdentifiers["BlockNumber"] == "" {
		return ""
	}
	blockNumber := journey.OtherIdentifiers["BlockNumber"]
	if journey.DataSource != nil && journey.DataSource.DatasetID != "" {
		return "dataset\x00" + journey.DataSource.DatasetID + "\x00" + blockNumber
	}
	return "service\x00" + journey.ServiceRef + "\x00" + blockNumber
}

func applyBlockEstimates(entries []*DepartureBoard, dateTime time.Time, doEstimates bool, realtimeLookup *DepartureBoardRealtimeLookup) blockEstimateStats {
	stats := blockEstimateStats{}
	if !doEstimates || realtimeLookup == nil {
		return stats
	}

	candidates := make([]blockEstimateCandidate, 0)
	maxDepartureByBlock := make(map[string]time.Time)
	journeyByBlock := make(map[string]*Journey)
	for _, entry := range entries {
		if entry == nil || entry.Journey == nil || entry.Type != DepartureBoardRecordTypeScheduled {
			continue
		}
		minutesFromNow := entry.Time.Sub(dateTime).Minutes()
		if minutesFromNow < 0 || minutesFromNow > 45 {
			continue
		}
		blockKey := journeyBlockKey(entry.Journey)
		if blockKey == "" {
			continue
		}

		candidates = append(candidates, blockEstimateCandidate{entry: entry, blockKey: blockKey})
		journeyByBlock[blockKey] = entry.Journey
		if maximum, found := maxDepartureByBlock[blockKey]; !found || entry.Journey.DepartureTime.After(maximum) {
			maxDepartureByBlock[blockKey] = entry.Journey.DepartureTime
		}
	}
	stats.candidates = int64(len(candidates))
	if len(candidates) == 0 {
		return stats
	}

	// Performance Review [Set K] [PR015] [P2] (boards): The batched query fetches block members
	// without checking service-day availability. Each board candidate then copies/scans
	// its predecessor list and Redis loads all unique IDs selected for lookup.
	// Reuse a dataset/version/date-scoped block index, select eligible predecessors in
	// order with a bounded search, and verify their run dates before carrying delays.
	blockClauses := make([]bson.M, 0, len(journeyByBlock))
	for blockKey, journey := range journeyByBlock {
		clause := bson.M{
			"otheridentifiers.BlockNumber": journey.OtherIdentifiers["BlockNumber"],
			"departuretime":                bson.M{"$lt": maxDepartureByBlock[blockKey]},
		}
		if journey.DataSource != nil && journey.DataSource.DatasetID != "" {
			clause["datasource.datasetid"] = journey.DataSource.DatasetID
		} else {
			clause["serviceref"] = journey.ServiceRef
		}
		blockClauses = append(blockClauses, clause)
	}

	journeysCollection := database.GetCollection("journeys")
	opts := options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "primaryidentifier", Value: 1},
		{Key: "otheridentifiers.BlockNumber", Value: 1},
		{Key: "datasource.datasetid", Value: 1},
		{Key: "serviceref", Value: 1},
		{Key: "departuretime", Value: 1},
	})
	cursor, err := journeysCollection.Find(context.Background(), bson.M{"$or": blockClauses}, opts)
	if err != nil {
		log.Error().Err(err).Int("blocks", len(blockClauses)).Msg("Failed to batch query vehicle block journeys")
		return stats
	}
	defer cursor.Close(context.Background())

	blockJourneys := make(map[string][]blockJourneyReference, len(journeyByBlock))
	for cursor.Next(context.Background()) {
		var blockJourney Journey
		if err := cursor.Decode(&blockJourney); err != nil {
			log.Error().Err(err).Msg("Failed to decode block journey")
			continue
		}
		blockKey := journeyBlockKey(&blockJourney)
		if blockKey != "" {
			blockJourneys[blockKey] = append(blockJourneys[blockKey], blockJourneyReference{
				PrimaryIdentifier: blockJourney.PrimaryIdentifier,
				DepartureTime:     blockJourney.DepartureTime,
			})
		}
	}
	if err := cursor.Err(); err != nil {
		log.Error().Err(err).Msg("Failed while reading vehicle block journeys")
	}
	for blockKey := range blockJourneys {
		sort.Slice(blockJourneys[blockKey], func(i, j int) bool {
			return blockJourneys[blockKey][i].DepartureTime.Before(blockJourneys[blockKey][j].DepartureTime)
		})
	}

	realtimeStats := applyBlockRealtimeEstimates(candidates, blockJourneys, realtimeLookup)
	stats.blockJourneys = realtimeStats.blockJourneys
	stats.realtimeMatched = realtimeStats.realtimeMatched
	stats.estimated = realtimeStats.estimated
	return stats
}

func applyBlockRealtimeEstimates(candidates []blockEstimateCandidate, blockJourneys map[string][]blockJourneyReference, realtimeLookup *DepartureBoardRealtimeLookup) blockEstimateStats {
	stats := blockEstimateStats{candidates: int64(len(candidates))}
	if len(candidates) == 0 || realtimeLookup == nil {
		return stats
	}

	allPrecedingRefs := make([]string, 0)
	seenRefs := make(map[string]struct{})
	for index, candidate := range candidates {
		refs := precedingBlockJourneyRefs(blockJourneys[candidate.blockKey], candidate.entry.Journey)
		candidates[index].refs = refs
		stats.blockJourneys += int64(len(refs))
		for _, ref := range refs {
			if _, seen := seenRefs[ref]; seen {
				continue
			}
			seenRefs[ref] = struct{}{}
			allPrecedingRefs = append(allPrecedingRefs, ref)
		}
	}

	realtimeByJourneyID := map[string]*RealtimeJourney{}
	if realtimeLookup.FindByJourneyIDs != nil && len(allPrecedingRefs) > 0 {
		realtimeByJourneyID = realtimeLookup.FindByJourneyIDs(allPrecedingRefs)
	}
	for _, candidate := range candidates {
		var blockRealtimeJourney *RealtimeJourney
		for _, ref := range candidate.refs {
			if realtimeJourney := realtimeByJourneyID[ref]; realtimeJourney != nil {
				blockRealtimeJourney = realtimeJourney
				break
			}
		}
		if blockRealtimeJourney == nil {
			continue
		}

		stats.realtimeMatched++
		if blockRealtimeJourney.Offset > 0 {
			candidate.entry.Time = candidate.entry.Time.Add(blockRealtimeJourney.Offset)
			candidate.entry.Delayed = true
		}
		candidate.entry.Type = DepartureBoardRecordTypeEstimated
		stats.estimated++
	}

	return stats
}

// BoardDisplayReference describes optional display hydration after selection.
// It is request-only metadata and never part of public response views.
type BoardDisplayReference struct {
	StopRef string
	Stop    *Stop
	Arrival bool
}

func BoardDisplayReferenceFor(journey *Journey, realtime *RealtimeJourney, kind BoardType, wholeCancelled bool) *BoardDisplayReference {
	if journey == nil || len(journey.Path) == 0 {
		return nil
	}
	if kind.IsArrival() {
		p := journey.Path[0]
		if p == nil {
			return nil
		}
		return &BoardDisplayReference{p.OriginStopRef, p.OriginStop, true}
	}
	if wholeCancelled || realtime == nil {
		return nil
	}
	last := journey.Path[len(journey.Path)-1]
	if last == nil {
		return nil
	}
	terminal := realtime.RealtimeStop(last.DestinationStopRef, len(journey.Path))
	if terminal == nil || !terminal.Cancelled {
		return nil
	}
	for i := len(journey.Path) - 1; i >= 0; i-- {
		var ref string
		var stop *Stop
		if i == 0 {
			p := journey.Path[0]
			if p == nil {
				continue
			}
			ref, stop = p.OriginStopRef, p.OriginStop
		} else {
			p := journey.Path[i-1]
			if p == nil {
				continue
			}
			ref, stop = p.DestinationStopRef, p.DestinationStop
		}
		if update := realtime.RealtimeStop(ref, i); update != nil && update.Cancelled {
			continue
		}
		return &BoardDisplayReference{ref, stop, false}
	}
	return nil
}
