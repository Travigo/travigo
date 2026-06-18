package ctdf

import (
	"context"
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

func GenerateDepartureBoardFromJourneys(journeys []*Journey, stopRefs []string, dateTime time.Time, doEstimates bool) []*DepartureBoard {
	journeysCollection := database.GetCollection("journeys")
	realtimeJourneysCollection := database.GetCollection("realtime_journeys")
	realtimeActiveCutoffDate := GetActiveRealtimeJourneyCutOffDate()

	journeys = FilterIdenticalJourneys(journeys, true)

	stopRefsSet := make(map[string]struct{}, len(stopRefs))
	for _, stopRef := range stopRefs {
		stopRefsSet[stopRef] = struct{}{}
	}

	// Batch the per-journey realtime_journeys FindOne (previously one
	// Mongo round-trip per journey via journey.GetRealtimeJourney) into a single Find with
	// $in over journey.primaryidentifier. The original projection is preserved exactly, with
	// "journey.primaryidentifier" added (additive only) so results can be keyed back to their
	// journey. The per-journey IsActive() gate is still applied below to preserve exact
	// semantics (IsActive has time.Now()/GetDestinationStop side-effects we do not replicate).
	realtimeJourneyProjection := bson.D{
		bson.E{Key: "activelytracked", Value: 1},
		bson.E{Key: "modificationdatetime", Value: 1},
		bson.E{Key: "timeoutdurationminutes", Value: 1},
		bson.E{Key: "stops", Value: 1},
		// bson.E{Key: "stops.*.cancelled", Value: 1},
		// bson.E{Key: "stops.*.platform", Value: 1},
		// bson.E{Key: "stops.*.departuretime", Value: 1},
		bson.E{Key: "cancelled", Value: 1},
		bson.E{Key: "vehiclelocation", Value: 1},
		bson.E{Key: "journey.primaryidentifier", Value: 1},
		bson.E{Key: "journey.path.destinationstopref", Value: 1},
		bson.E{Key: "journey.path.destinationarrivaltime", Value: 1},
	}

	journeyPrimaryIdentifiers := make([]string, 0, len(journeys))
	for _, journey := range journeys {
		journeyPrimaryIdentifiers = append(journeyPrimaryIdentifiers, journey.PrimaryIdentifier)
	}

	realtimeJourneysByJourneyID := map[string]*RealtimeJourney{}
	if len(journeyPrimaryIdentifiers) > 0 {
		realtimeCursor, err := realtimeJourneysCollection.Find(context.Background(), bson.M{
			"journey.primaryidentifier": bson.M{"$in": journeyPrimaryIdentifiers},
			"modificationdatetime":      bson.M{"$gt": realtimeActiveCutoffDate},
		}, options.Find().SetProjection(realtimeJourneyProjection))
		if err != nil {
			log.Error().Err(err).Msg("Failed to query realtime journeys")
		} else {
			for realtimeCursor.Next(context.Background()) {
				var realtimeJourney RealtimeJourney
				if err := realtimeCursor.Decode(&realtimeJourney); err != nil {
					log.Error().Err(err).Msg("Failed to decode RealtimeJourney")
					continue
				}

				if realtimeJourney.Journey != nil {
					rj := realtimeJourney
					realtimeJourneysByJourneyID[realtimeJourney.Journey.PrimaryIdentifier] = &rj
				}
			}
		}
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
							return nil
						}

						break
					}
				}

				if prefetchedRealtimeJourney := realtimeJourneysByJourneyID[journey.PrimaryIdentifier]; prefetchedRealtimeJourney != nil && prefetchedRealtimeJourney.IsActive() {
					journey.RealtimeJourney = prefetchedRealtimeJourney
				}

				for _, path := range journey.Path {
					if _, ok := stopRefsSet[path.OriginStopRef]; ok {
						refTime := path.OriginDepartureTime
						stopPlatform = path.OriginPlatform
						stopPlatformType = "ESTIMATED"

						// Ignore drop off only stops from the departure board as no one should be getting onto the vehicle at this point
						if len(path.OriginActivity) == 1 && path.OriginActivity[0] == JourneyPathItemActivitySetdown {
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

						stopDepartureTime = time.Date(
							dateTime.Year(), dateTime.Month(), dateTime.Day(), refTime.Hour(), refTime.Minute(), refTime.Second(), refTime.Nanosecond(), dateTime.Location(),
						)

						destinationDisplay = path.DestinationDisplay
						break
					}
				}

				if stopDepartureTime.Before(dateTime) {
					return nil
				}

				if journey.DetailedRailInformation != nil && journey.DetailedRailInformation.ReplacementBus {
					stopPlatform = "BUS"
					stopPlatformType = "ACTUAL"
				}

				// TODO(high-risk): This block does a Find (block journeys by
				// serviceref + BlockNumber) plus a FindOne (realtime by journeyref $in) per
				// qualifying journey. It cannot be cleanly pre-batched because qualification
				// (doEstimates + Scheduled + within 45 min + BlockNumber present) depends on
				// stopDepartureTime and departureBoardRecordType which are only known after the
				// path loop above. A batch approach would be: after a first pass computes which
				// journeys qualify, group by (serviceRef, BlockNumber) to issue one journeys Find
				// with $in, then one realtime_journeys Find with $in over all collected journeyrefs,
				// mapping offsets back per block. Deferred to preserve exact behaviour.
				// If the departure is within 45 minutes then attempt to do an estimated arrival based on current vehicle realtime journey
				// We estimate the current vehicle realtime journey based on the Block Number
				stopDepartureTimeFromNow := stopDepartureTime.Sub(dateTime).Minutes()
				if doEstimates &&
					departureBoardRecordType == DepartureBoardRecordTypeScheduled &&
					stopDepartureTimeFromNow <= 45 && stopDepartureTimeFromNow >= 0 &&
					journey.OtherIdentifiers["BlockNumber"] != "" {

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

					var blockRealtimeJourney *RealtimeJourney
					realtimeJourneysCollection.FindOne(context.Background(),
						bson.M{
							"journeyref": bson.M{
								"$in": blockJourneys,
							},
							"modificationdatetime": bson.M{"$gt": realtimeActiveCutoffDate},
						}, &options.FindOneOptions{}).Decode(&blockRealtimeJourney)

					if blockRealtimeJourney != nil {
						// Ignore negative offsets as we assume bus will right itself when turning over
						if blockRealtimeJourney.Offset.Minutes() > 0 {
							stopDepartureTime = stopDepartureTime.Add(blockRealtimeJourney.Offset)
						}
						departureBoardRecordType = DepartureBoardRecordTypeEstimated
					}
				}

				// TODO(high-risk): GetDestinationStop() and GetService() each issue a
				// per-journey FindOne. A batch plan would pre-fetch, for every journey's last path
				// item, the destination stop and the service via two Find($in) calls before the
				// pool, keyed into maps. This is not cleanly replicable here because both lookups
				// match on either primaryidentifier OR otheridentifiers (otheridentifiers is an
				// array, so a simple key->doc map can miss matches), GetService() mutates
				// journey.Service which is consumed downstream, and UpdateNameFromServiceOverrides
				// mutates the stop name. Deferred to preserve exact behaviour and output.
				if destinationDisplay == "" {
					lastPathItem := journey.Path[len(journey.Path)-1]
					lastPathItem.GetDestinationStop()

					if lastPathItem.DestinationStop == nil {
						destinationDisplay = "See Vehicle"
					} else {
						journey.GetService()
						lastPathItem.DestinationStop.UpdateNameFromServiceOverrides(journey.Service)

						destinationDisplay = lastPathItem.DestinationStop.PrimaryName
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

	return departureBoard
}
