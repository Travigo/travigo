package ctdf

import (
	"context"
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
	ByJourneyID       map[string]*RealtimeJourney
	FindByJourneyRefs func(journeyRefs []string) *RealtimeJourney
}

func GenerateDepartureBoardFromJourneys(journeys []*Journey, stopRefs []string, dateTime time.Time, doEstimates bool, realtimeLookup *DepartureBoardRealtimeLookup) []*DepartureBoard {
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
	var setdownSkippedCount atomic.Int64
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
			var stopDepartureTime time.Time
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
				// Don't even think about it if we're passed 4 hours departure on this stop
				for _, path := range journey.Path {
					if _, ok := stopRefsSet[path.OriginStopRef]; ok {
						journeyDepMins := (path.OriginDepartureTime.Hour() * 60) + path.OriginDepartureTime.Minute()
						startMins := (dateTime.Hour() * 60) + dateTime.Minute()

						if (startMins - journeyDepMins) > 240 {
							tooOldSkippedCount.Add(1)
							return nil
						}

						break
					}
				}

				if prefetchedRealtimeJourney := realtimeLookup.ByJourneyID[journey.PrimaryIdentifier]; prefetchedRealtimeJourney != nil && prefetchedRealtimeJourney.IsActive() {
					journey.RealtimeJourney = prefetchedRealtimeJourney
					prefetchedRealtimeAppliedCount.Add(1)
				}

				for _, path := range journey.Path {
					if _, ok := stopRefsSet[path.OriginStopRef]; ok {
						stopMatchedCount.Add(1)
						refTime := path.OriginDepartureTime
						stopPlatform = path.OriginPlatform
						stopPlatformType = "ESTIMATED"

						// Ignore drop off only stops from the departure board as no one should be getting onto the vehicle at this point
						if len(path.OriginActivity) == 1 && path.OriginActivity[0] == JourneyPathItemActivitySetdown {
							setdownSkippedCount.Add(1)
							return nil
						}

						// Use the realtime estimated stop time based if realtime is available
						if journey.RealtimeJourney != nil {
							var realtimeJourneyStop *RealtimeJourneyStops

							// Lookup realtime journey stop by either direct reference or other identifiers (which requires extra db call)
							if journey.RealtimeJourney.Stops[path.OriginStopRef] != nil {
								realtimeJourneyStop = journey.RealtimeJourney.Stops[path.OriginStopRef]
							} else {
								path.GetOriginStop()

								for stopID, potentialRealtimeJourneyStop := range journey.RealtimeJourney.Stops {
									if path.OriginStop.PrimaryIdentifier == stopID || slices.Contains(path.OriginStop.OtherIdentifiers, stopID) {
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
									refTime = realtimeJourneyStop.DepartureTime
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

						if journey.RealtimeJourney != nil && journey.RealtimeJourney.Cancelled {
							departureBoardRecordType = DepartureBoardRecordTypeCancelled
						}
						if departureBoardRecordType == DepartureBoardRecordTypeCancelled {
							cancelledCount.Add(1)
						}

						stopDepartureTime = time.Date(
							dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
						)

						destinationDisplay = path.DestinationDisplay
						break
					}
				}

				if stopDepartureTime.Before(dateTime) {
					beforeStartSkippedCount.Add(1)
					return nil
				}

				if journey.DetailedRailInformation != nil && journey.DetailedRailInformation.ReplacementBus {
					stopPlatform = "BUS"
					stopPlatformType = "ACTUAL"
					replacementBusCount.Add(1)
				}

				// TODO(high-risk): This block does a Find (block journeys by
				// serviceref + BlockNumber) plus a realtime lookup by journeyref $in per
				// qualifying journey. It cannot be cleanly pre-batched because qualification
				// (doEstimates + Scheduled + within 45 min + BlockNumber present) depends on
				// stopDepartureTime and departureBoardRecordType which are only known after the
				// path loop above. A batch approach would be: after a first pass computes which
				// journeys qualify, group by (serviceRef, BlockNumber) to issue one journeys Find
				// with $in, then one realtime lookup with $in over all collected journeyrefs,
				// mapping offsets back per block. Deferred to preserve exact behaviour.
				// If the departure is within 45 minutes then attempt to do an estimated arrival based on current vehicle realtime journey
				// We estimate the current vehicle realtime journey based on the Block Number
				stopDepartureTimeFromNow := stopDepartureTime.Sub(dateTime).Minutes()
				if doEstimates &&
					departureBoardRecordType == DepartureBoardRecordTypeScheduled &&
					stopDepartureTimeFromNow <= 45 && stopDepartureTimeFromNow >= 0 &&
					journey.OtherIdentifiers["BlockNumber"] != "" {
					estimateCandidateCount.Add(1)

					var blockJourneys []string
					opts := options.Find().SetProjection(bson.D{
						bson.E{Key: "primaryidentifier", Value: 1},
					})
					cursor, _ := journeysCollection.Find(context.Background(), bson.M{"serviceref": journey.ServiceRef, "otheridentifiers.BlockNumber": journey.OtherIdentifiers["BlockNumber"]}, opts)

					for cursor.Next(context.Background()) {
						var blockJourney Journey
						err := cursor.Decode(&blockJourney)
						if err != nil {
							log.Error().Err(err).Msg("Failed to decode Journey")
						}

						blockJourneys = append(blockJourneys, blockJourney.PrimaryIdentifier)
					}
					estimateBlockJourneyCount.Add(int64(len(blockJourneys)))

					var blockRealtimeJourney *RealtimeJourney
					if realtimeLookup.FindByJourneyRefs != nil && len(blockJourneys) > 0 {
						blockRealtimeJourney = realtimeLookup.FindByJourneyRefs(blockJourneys)
					}

					if blockRealtimeJourney != nil {
						estimateRealtimeMatchedCount.Add(1)
						// Ignore negative offsets as we assume bus will right itself when turning over
						if blockRealtimeJourney.Offset.Minutes() > 0 {
							stopDepartureTime = stopDepartureTime.Add(blockRealtimeJourney.Offset)
						}
						departureBoardRecordType = DepartureBoardRecordTypeEstimated
						estimatedCount.Add(1)
					}
				}

				return &DepartureBoard{
					Journey:            journey,
					Time:               stopDepartureTime,
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
		Int64("setdown_skipped", setdownSkippedCount.Load()).
		Int64("realtime_stop_matched", realtimeStopMatchedCount.Load()).
		Int64("cancelled_records", cancelledCount.Load()).
		Int64("before_start_skipped", beforeStartSkippedCount.Load()).
		Int64("replacement_bus_records", replacementBusCount.Load()).
		Int64("estimate_candidates", estimateCandidateCount.Load()).
		Int64("estimate_block_journeys", estimateBlockJourneyCount.Load()).
		Int64("estimate_realtime_matched", estimateRealtimeMatchedCount.Load()).
		Int64("estimated_records", estimatedCount.Load()).
		Int("nil_results", len(departureBoardWithNil)-len(departureBoard)).
		Int("generated_departures", len(departureBoard)).
		Dur("duration", time.Since(generationStart)).
		Msg("Departure board generation stats")

	return departureBoard
}
