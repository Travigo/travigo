package ctdf

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sourcegraph/conc/pool"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
	"golang.org/x/exp/slices"
)

type DepartureBoard struct {
	Journey            *Journey                 `groups:"basic,departures-llm"`
	DestinationDisplay string                   `groups:"basic,departures-llm"`
	Type               DepartureBoardRecordType `groups:"basic,departures-llm"`

	Platform     string `groups:"basic,departures-llm"`
	PlatformType string `groups:"basic,departures-llm"`

	Time time.Time `groups:"basic,departures-llm"`
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
	FindByJourneyRefs   func(journeyRefs []string) *RealtimeJourney
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
		journeyID := entry.Journey.PrimaryIdentifier
		if _, seen := seenJourneyIDs[journeyID]; seen {
			continue
		}
		seenJourneyIDs[journeyID] = struct{}{}
		deduplicated = append(deduplicated, entry)
	}
	return deduplicated
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

func boardPathStop(path *JourneyPathItem, boardType BoardType) *Stop {
	if boardType.IsArrival() {
		path.GetDestinationStop()
		return path.DestinationStop
	}
	path.GetOriginStop()
	return path.OriginStop
}

func boardRealtimeStopTime(stop *RealtimeJourneyStops, boardType BoardType) time.Time {
	if boardType.IsArrival() {
		return stop.ArrivalTime
	}
	return stop.DepartureTime
}

// BoardDestinationDisplay returns the service destination for departures and
// the journey origin for arrivals.
func BoardDestinationDisplay(journey *Journey, fallback string, boardType BoardType) string {
	if !boardType.IsArrival() || journey == nil || len(journey.Path) == 0 {
		return fallback
	}

	firstPathItem := journey.Path[0]
	if firstPathItem == nil {
		return fallback
	}
	firstPathItem.GetOriginStop()
	if firstPathItem.OriginStop != nil && firstPathItem.OriginStop.PrimaryName != "" {
		return firstPathItem.OriginStop.PrimaryName
	}

	return firstPathItem.OriginStopRef
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
	generationStart := time.Now()
	inputJourneyCount := len(journeys)
	journeysCollection := database.GetCollection("journeys")

	journeys = FilterIdenticalJourneys(journeys, true)
	uniqueJourneyCount := len(journeys)
	if realtimeLookup == nil {
		realtimeLookup = &DepartureBoardRealtimeLookup{}
	}

	var availabilityMatchedCount atomic.Int64
	var tooOldSkippedCount atomic.Int64
	var prefetchedRealtimeAppliedCount atomic.Int64
	var stopMatchedCount atomic.Int64
	var activitySkippedCount atomic.Int64
	var realtimeStopMatchedCount atomic.Int64
	var cancelledCount atomic.Int64
	var beforeStartSkippedCount atomic.Int64
	var replacementBusCount atomic.Int64
	var estimateCandidateCount atomic.Int64
	var estimateBlockJourneyCount atomic.Int64
	var estimateRealtimeMatchedCount atomic.Int64
	var estimatedCount atomic.Int64

	stopRefsSet := make(map[string]struct{}, len(stopRefs))
	for _, stopRef := range stopRefs {
		stopRefsSet[stopRef] = struct{}{}
	}

	// Journeys within the same vehicle block share the same block-journey list
	// and the same block realtime journey. Memoise both per (serviceRef,
	// blockNumber) so that qualifying journeys sharing a block don't each issue
	// duplicate Mongo/realtime lookups during this generation.
	var (
		blockJourneysCache sync.Map // blockKey -> []string
		blockRealtimeCache sync.Map // blockKey -> *RealtimeJourney (may be nil)
	)

	p := pool.NewWithResults[*DepartureBoard]()
	maxGoroutines := 200
	if len(journeys) < maxGoroutines {
		maxGoroutines = len(journeys)
	}
	if maxGoroutines < 1 {
		maxGoroutines = 1
	}
	p.WithMaxGoroutines(maxGoroutines)

	for _, journey := range journeys {
		p.Go(func() *DepartureBoard {
			var stopTime time.Time
			var stopPlatform string
			var stopPlatformType string
			var destinationDisplay string
			departureBoardRecordType := DepartureBoardRecordTypeScheduled

			if journey.Availability.MatchDate(dateTime) {
				availabilityMatchedCount.Add(1)
				// TODO(medium-risk): This 4-hour-cutoff loop and the detail-extraction
				// loop below both scan journey.Path and break on the first stopRef match. They could
				// be merged into a single pass, but the realtime-journey assignment currently sits
				// between them, and the cutoff loop's early `return nil` must run before any detail
				// work. Merging would reorder the realtime assignment relative to the cutoff check;
				// left as two passes to preserve identical behaviour and early-return ordering.
				// Do not include board entries that are more than four hours old.
				for _, path := range journey.Path {
					if _, ok := stopRefsSet[boardPathStopRef(path, boardType)]; ok {
						journeyTime := boardPathTime(path, boardType)
						journeyDepMins := (journeyTime.Hour() * 60) + journeyTime.Minute()
						startMins := (dateTime.Hour() * 60) + dateTime.Minute()

						if (startMins - journeyDepMins) > 240 {
							tooOldSkippedCount.Add(1)
							return nil
						}

						break
					}
				}

				if prefetchedRealtimeJourney := realtimeLookup.ByJourneyID[journey.PrimaryIdentifier]; prefetchedRealtimeJourney != nil && (prefetchedRealtimeJourney.IsActive() || prefetchedRealtimeJourney.Cancelled) {
					journey.RealtimeJourney = prefetchedRealtimeJourney
					prefetchedRealtimeAppliedCount.Add(1)
				}

				for _, path := range journey.Path {
					if _, ok := stopRefsSet[boardPathStopRef(path, boardType)]; ok {
						stopMatchedCount.Add(1)
						refTime := boardPathTime(path, boardType)
						stopPlatform = boardPathPlatform(path, boardType)
						stopPlatformType = "ESTIMATED"

						// A departure needs pickup permission; an arrival needs setdown permission.
						if boardPathIsUnavailable(path, boardType) {
							activitySkippedCount.Add(1)
							return nil
						}

						// Use the realtime estimated stop time based if realtime is available
						if journey.RealtimeJourney != nil {
							var realtimeJourneyStop *RealtimeJourneyStops

							// Lookup realtime journey stop by either direct reference or other identifiers (which requires extra db call)
							if journey.RealtimeJourney.Stops[boardPathStopRef(path, boardType)] != nil {
								realtimeJourneyStop = journey.RealtimeJourney.Stops[boardPathStopRef(path, boardType)]
							} else {
								boardStop := boardPathStop(path, boardType)

								for stopID, potentialRealtimeJourneyStop := range journey.RealtimeJourney.Stops {
									if boardStop != nil && (boardStop.PrimaryIdentifier == stopID || slices.Contains(boardStop.OtherIdentifiers, stopID)) {
										realtimeJourneyStop = potentialRealtimeJourneyStop
										break
									}
								}
							}

							if realtimeJourneyStop != nil {
								realtimeStopMatchedCount.Add(1)
								if realtimeJourneyStop.Cancelled {
									departureBoardRecordType = DepartureBoardRecordTypeCancelled
								}

								if journey.RealtimeJourney.ActivelyTracked {
									refTime = boardRealtimeStopTime(realtimeJourneyStop, boardType)
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

						if IsBoardJourneyCancelled(journey, journey.RealtimeJourney, realtimeLookup.CancelledJourneyIDs) {
							departureBoardRecordType = DepartureBoardRecordTypeCancelled
						}
						if departureBoardRecordType == DepartureBoardRecordTypeCancelled {
							cancelledCount.Add(1)
						}

						stopTime = time.Date(
							dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
						)

						destinationDisplay = BoardDestinationDisplay(journey, path.DestinationDisplay, boardType)
						break
					}
				}

				if stopTime.Before(dateTime) {
					beforeStartSkippedCount.Add(1)
					return nil
				}

				if journey.DetailedRailInformation != nil && journey.DetailedRailInformation.ReplacementBus {
					stopPlatform = "BUS"
					stopPlatformType = "ACTUAL"
					replacementBusCount.Add(1)
				}

				// This block does a Find (block journeys by serviceref + BlockNumber)
				// plus a realtime lookup per qualifying journey. Qualification
				// (doEstimates + Scheduled + within 45 min + BlockNumber present) depends on
				// stopTime and departureBoardRecordType which are only known after the
				// path loop above, so it can't be fully hoisted into a pre-pass. As a cheaper
				// mitigation, both lookups are memoised per (serviceRef, BlockNumber) via
				// blockJourneysCache/blockRealtimeCache so journeys sharing a block reuse the
				// result instead of each issuing duplicate Mongo/realtime round trips.
				// A fuller batch (first pass to collect qualifiers, then one Find with $in over
				// all blocks and one realtime lookup over all refs) remains possible.
				// If the board entry is within 45 minutes then attempt to estimate it based on current vehicle realtime journey.
				// We estimate the current vehicle realtime journey based on the Block Number
				stopDepartureTimeFromNow := stopTime.Sub(dateTime).Minutes()
				if doEstimates &&
					departureBoardRecordType == DepartureBoardRecordTypeScheduled &&
					stopDepartureTimeFromNow <= 45 && stopDepartureTimeFromNow >= 0 &&
					journey.OtherIdentifiers["BlockNumber"] != "" {
					estimateCandidateCount.Add(1)

					blockNumber := journey.OtherIdentifiers["BlockNumber"]
					blockKey := journey.ServiceRef + "\x00" + blockNumber

					var blockJourneys []string
					if cached, ok := blockJourneysCache.Load(blockKey); ok {
						blockJourneys = cached.([]string)
					} else {
						opts := options.Find().SetProjection(bson.D{
							bson.E{Key: "primaryidentifier", Value: 1},
						})
						cursor, _ := journeysCollection.Find(context.Background(), bson.M{"serviceref": journey.ServiceRef, "otheridentifiers.BlockNumber": blockNumber}, opts)

						for cursor.Next(context.Background()) {
							var blockJourney Journey
							err := cursor.Decode(&blockJourney)
							if err != nil {
								log.Error().Err(err).Msg("Failed to decode Journey")
							}

							blockJourneys = append(blockJourneys, blockJourney.PrimaryIdentifier)
						}

						blockJourneysCache.Store(blockKey, blockJourneys)
					}
					estimateBlockJourneyCount.Add(int64(len(blockJourneys)))

					var blockRealtimeJourney *RealtimeJourney
					if realtimeLookup.FindByJourneyRefs != nil && len(blockJourneys) > 0 {
						if cached, ok := blockRealtimeCache.Load(blockKey); ok {
							blockRealtimeJourney = cached.(*RealtimeJourney)
						} else {
							blockRealtimeJourney = realtimeLookup.FindByJourneyRefs(blockJourneys)
							blockRealtimeCache.Store(blockKey, blockRealtimeJourney)
						}
					}

					if blockRealtimeJourney != nil {
						estimateRealtimeMatchedCount.Add(1)
						// Ignore negative offsets as we assume bus will right itself when turning over
						if blockRealtimeJourney.Offset.Minutes() > 0 {
							stopTime = stopTime.Add(blockRealtimeJourney.Offset)
						}
						departureBoardRecordType = DepartureBoardRecordTypeEstimated
						estimatedCount.Add(1)
					}
				}

				return &DepartureBoard{
					Journey:            journey,
					Time:               stopTime,
					DestinationDisplay: destinationDisplay,
					Type:               departureBoardRecordType,
					Platform:           stopPlatform,
					PlatformType:       stopPlatformType,
				}
			}

			return nil
		})
	}

	departureBoardWithNil := p.Wait()
	departureBoard := make([]*DepartureBoard, 0, len(departureBoardWithNil))

	for _, i := range departureBoardWithNil {
		if i != nil {
			departureBoard = append(departureBoard, i)
		}
	}
	departureBoard = DeduplicateBoardEntries(departureBoard)

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
		Int64("stop_matched", stopMatchedCount.Load()).
		Int64("activity_skipped", activitySkippedCount.Load()).
		Int64("realtime_stop_matched", realtimeStopMatchedCount.Load()).
		Int64("cancelled_records", cancelledCount.Load()).
		Int64("before_start_skipped", beforeStartSkippedCount.Load()).
		Int64("replacement_bus_records", replacementBusCount.Load()).
		Int64("estimate_candidates", estimateCandidateCount.Load()).
		Int64("estimate_block_journeys", estimateBlockJourneyCount.Load()).
		Int64("estimate_realtime_matched", estimateRealtimeMatchedCount.Load()).
		Int64("estimated_records", estimatedCount.Load()).
		Int("nil_results", len(departureBoardWithNil)-len(departureBoard)).
		Str("board_type", string(boardType)).
		Int("generated_entries", len(departureBoard)).
		Dur("duration", time.Since(generationStart)).
		Msg("Departure board generation stats")

	return departureBoard
}
