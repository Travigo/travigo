package tfl

import (
	"context"
	"math"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/dataaggregator/source"
	"github.com/travigo/travigo/pkg/dataaggregator/source/localdepartureboard"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"github.com/travigo/travigo/pkg/transforms"
	"github.com/travigo/travigo/pkg/util"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	tflRealtimeBoardHorizon        = 2 * time.Hour
	tflRealtimeBoardMinimumPerStop = 100
	tflRealtimeBoardMaximumPerStop = 500
)

var (
	backfillSource     localdepartureboard.Source
	backfillSourceOnce sync.Once
)

func getBackfillSource() *localdepartureboard.Source {
	backfillSourceOnce.Do(func() {
		backfillSource.Setup()
	})
	return &backfillSource
}

func (s Source) DepartureBoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	q.Type = ctdf.BoardTypeDeparture
	return s.BoardQuery(q)
}

// BoardQuery uses TfL's per-stop predicted-arrival feed for both board modes.
// TfL does not provide a separate predicted-departure timestamp in this feed;
// retaining this behaviour keeps departure responses compatible while allowing
// arrivals to use the same realtime data and scheduled backfill.
func (s Source) BoardQuery(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	tflOperator := &ctdf.Operator{
		PrimaryIdentifier: "gb-noc-TFLO",
		PrimaryName:       "Transport for London",
	}

	now := time.Now()

	isTFLStop := false
	var services []*ctdf.Service
	services, _ = dataaggregator.Lookup[[]*ctdf.Service](query.ServicesByStop{
		Stop: q.Stop,
	})

	for _, service := range services {
		if service.OperatorRef == tflOperator.PrimaryIdentifier {
			isTFLStop = true
			break
		}
	}

	log.Debug().Str("Length", time.Now().Sub(now).String()).Msg("Check if TfL service")

	if !isTFLStop {
		return nil, source.UnsupportedSourceError
	}

	var departureBoard []*ctdf.DepartureBoard

	now = time.Now()
	directionWatermarks := map[boardDirectionKey]time.Time{}

	stopTimezone, err := time.LoadLocation(q.Stop.Timezone)
	if err != nil || stopTimezone == nil {
		stopTimezone = time.UTC
	}

	allStopIDS := q.Stop.GetAllStopIDs()
	stopIDsSet := make(map[string]struct{}, len(allStopIDS))
	for _, stopID := range allStopIDS {
		stopIDsSet[stopID] = struct{}{}
	}
	realtimePerStopLimit := q.Count * 4
	if realtimePerStopLimit < tflRealtimeBoardMinimumPerStop {
		realtimePerStopLimit = tflRealtimeBoardMinimumPerStop
	}
	if realtimePerStopLimit > tflRealtimeBoardMaximumPerStop {
		realtimePerStopLimit = tflRealtimeBoardMaximumPerStop
	}
	realtimeJourneys, err := realtimestore.FindTFLDepartureBoardJourneysBounded(
		context.Background(),
		allStopIDS,
		now.Add(-30*time.Second),
		now.Add(tflRealtimeBoardHorizon),
		int64(realtimePerStopLimit),
	)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query TfL realtime journeys")
	}
	journeyIDs := make([]string, 0, len(realtimeJourneys))
	for index := range realtimeJourneys {
		if realtimeJourneys[index].Journey != nil {
			journeyIDs = append(journeyIDs, realtimeJourneys[index].Journey.PrimaryIdentifier)
		}
	}
	cancelledJourneyIDs, err := ctdf.ActiveJourneyCancellationAlertIDs(context.Background(), journeyIDs, q.StartDateTime, now)
	if err != nil {
		log.Error().Err(err).Msg("Failed to query journey cancellation service alerts")
	}
	servicesByRef := loadTFLBoardServices(services, realtimeJourneys)
	transforms.Transform(tflOperator, 2)

	log.Debug().Str("Length", time.Now().Sub(now).String()).Msg("Query TfL realtime journeys")

	generateDeparteBoardStart := time.Now()

	for _, realtimeJourney := range realtimeJourneys {
		timedOut := (now.Sub(realtimeJourney.ModificationDateTime)).Minutes() > 2

		if !timedOut {
			var realtimeJourneyStop *ctdf.RealtimeJourneyStops
			for _, candidate := range realtimeJourney.Stops {
				if candidate == nil || candidate.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
					continue
				}
				if _, matchesStop := stopIDsSet[candidate.StopRef]; !matchesStop {
					continue
				}
				if realtimeJourneyStop == nil || candidate.ArrivalTime.Before(realtimeJourneyStop.ArrivalTime) {
					realtimeJourneyStop = candidate
				}
			}
			if realtimeJourneyStop == nil || realtimeJourneyStop.TimeType != ctdf.RealtimeJourneyStopTimeEstimatedFuture {
				continue
			}

			scheduledTime := realtimeJourneyStop.ArrivalTime.In(stopTimezone)

			// Skip over this one if we've already past its arrival time (allow 30 second overlap)
			if scheduledTime.Before(now.Add(-30 * time.Second)) {
				continue
			}

			wholeJourneyCancelled := ctdf.IsBoardJourneyCancelled(realtimeJourney.Journey, &realtimeJourney, cancelledJourneyIDs)
			departure := &ctdf.DepartureBoard{
				DestinationDisplay: ctdf.BoardDestinationDisplayWithRealtime(realtimeJourney.Journey, &realtimeJourney, realtimeJourney.Journey.DestinationDisplay, q.Type, wholeJourneyCancelled),
				Type:               ctdf.DepartureBoardRecordTypeRealtimeTracked,
				Time:               scheduledTime,

				Journey: realtimeJourney.Journey,
			}
			if wholeJourneyCancelled {
				departure.Type = ctdf.DepartureBoardRecordTypeCancelled
			}
			if realtimeJourney.Journey.Service == nil {
				realtimeJourney.Journey.Service = servicesByRef[realtimeJourney.Journey.ServiceRef]
			}
			departure.Journey.Operator = tflOperator
			departure.Journey.OperatorRef = tflOperator.PrimaryIdentifier

			platform := realtimeJourneyStop.Platform

			if platform != "" {
				departure.Platform = platform
				departure.PlatformType = "ACTUAL"
			}

			departureBoard = append(departureBoard, departure)

			if direction, ok := departureBoardDirection(departure, allStopIDS, q.Type); ok {
				if watermark := directionWatermarks[direction]; scheduledTime.After(watermark) {
					directionWatermarks[direction] = scheduledTime
				}
			}
		}
	}

	log.Debug().Str("Length", time.Now().Sub(generateDeparteBoardStart).String()).Msg("Generate TfL departure board from realtime journeys")
	departureBoard = ctdf.DeduplicateBoardEntries(departureBoard)

	// Load scheduled journeys from the requested time for every direction. Each
	// direction is then advanced only as far as its own realtime predictions,
	// so a later prediction in one direction cannot hide an earlier scheduled
	// journey in another.
	localSource := getBackfillSource()
	localDepartures, err := localSource.Lookup(q)

	if err == nil {
		scheduledBackfill := filterScheduledBoardByDirection(
			localDepartures.([]*ctdf.DepartureBoard),
			allStopIDS,
			q.Type,
			directionWatermarks,
		)
		departureBoard = append(departureBoard, scheduledBackfill...)
	}

	return ctdf.DeduplicateBoardEntries(departureBoard), nil
}

func loadTFLBoardServices(services []*ctdf.Service, realtimeJourneys []ctdf.RealtimeJourney) map[string]*ctdf.Service {
	servicesByRef := make(map[string]*ctdf.Service, len(services))
	serviceRefs := map[string]struct{}{}
	for _, service := range services {
		if service == nil {
			continue
		}
		transforms.Transform(service, 2)
		servicesByRef[service.PrimaryIdentifier] = service
	}

	for index := range realtimeJourneys {
		journey := realtimeJourneys[index].Journey
		if journey == nil {
			continue
		}
		if journey.Service != nil {
			transforms.Transform(journey.Service, 2)
			servicesByRef[journey.ServiceRef] = journey.Service
		} else if journey.ServiceRef != "" && servicesByRef[journey.ServiceRef] == nil {
			serviceRefs[journey.ServiceRef] = struct{}{}
		}
	}
	if len(serviceRefs) == 0 {
		return servicesByRef
	}

	identifiers := make([]string, 0, len(serviceRefs))
	for identifier := range serviceRefs {
		identifiers = append(identifiers, identifier)
	}
	opts := options.Find().SetProjection(bson.D{
		{Key: "_id", Value: 0},
		{Key: "creationdatetime", Value: 0},
		{Key: "modificationdatetime", Value: 0},
		{Key: "datasource", Value: 0},
		{Key: "routes", Value: 0},
	})
	cursor, err := database.GetCollection("services").Find(context.Background(), bson.M{
		"$or": bson.A{
			bson.M{"primaryidentifier": bson.M{"$in": identifiers}},
			bson.M{"otheridentifiers": bson.M{"$in": identifiers}},
		},
	}, opts)
	if err != nil {
		log.Error().Err(err).Int("service_refs", len(identifiers)).Msg("Failed to batch load TfL departure board services")
		return servicesByRef
	}
	defer cursor.Close(context.Background())

	for cursor.Next(context.Background()) {
		var service ctdf.Service
		if err := cursor.Decode(&service); err != nil {
			log.Error().Err(err).Msg("Failed to decode TfL departure board service")
			continue
		}
		transforms.Transform(&service, 2)
		servicesByRef[service.PrimaryIdentifier] = &service
		for _, identifier := range service.OtherIdentifiers {
			servicesByRef[identifier] = &service
		}
	}
	if err := cursor.Err(); err != nil {
		log.Error().Err(err).Msg("Failed while reading TfL departure board services")
	}
	return servicesByRef
}

type boardDirectionKey struct {
	serviceRef      string
	adjacentStopRef string
}

func departureBoardDirection(entry *ctdf.DepartureBoard, stopIDs []string, boardType ctdf.BoardType) (boardDirectionKey, bool) {
	if entry == nil || entry.Journey == nil {
		return boardDirectionKey{}, false
	}

	serviceRef := entry.Journey.ServiceRef
	if serviceRef == "" && entry.Journey.Service != nil {
		serviceRef = entry.Journey.Service.PrimaryIdentifier
	}
	if serviceRef == "" {
		return boardDirectionKey{}, false
	}

	var adjacentStopRef string
	closestTimeDifference := time.Duration(math.MaxInt64)
	for pathIndex, pathItem := range entry.Journey.Path {
		if pathItem == nil {
			continue
		}

		var stopRef string
		var candidateAdjacentStopRef string
		var candidateTime time.Time
		if boardType.IsArrival() {
			stopRef = pathItem.DestinationStopRef
			candidateAdjacentStopRef = pathItem.OriginStopRef
			candidateTime = pathItem.DestinationArrivalTime
			if candidateTime.IsZero() && pathIndex+1 < len(entry.Journey.Path) {
				nextPathItem := entry.Journey.Path[pathIndex+1]
				if nextPathItem != nil && nextPathItem.OriginStopRef == stopRef {
					candidateTime = nextPathItem.OriginArrivalTime
				}
			}
		} else {
			stopRef = pathItem.OriginStopRef
			candidateAdjacentStopRef = pathItem.DestinationStopRef
			candidateTime = pathItem.OriginDepartureTime
			if candidateTime.IsZero() {
				candidateTime = pathItem.OriginArrivalTime
			}
		}
		if !util.ContainsString(stopIDs, stopRef) || candidateAdjacentStopRef == "" {
			continue
		}

		timeDifference := time.Duration(math.MaxInt64)
		if !candidateTime.IsZero() {
			timeDifference = entry.Time.Sub(candidateTime).Abs()
		}
		if adjacentStopRef == "" || timeDifference < closestTimeDifference {
			adjacentStopRef = candidateAdjacentStopRef
			closestTimeDifference = timeDifference
		}
	}
	if adjacentStopRef == "" {
		return boardDirectionKey{}, false
	}

	return boardDirectionKey{
		serviceRef:      serviceRef,
		adjacentStopRef: adjacentStopRef,
	}, true
}

func filterScheduledBoardByDirection(
	entries []*ctdf.DepartureBoard,
	stopIDs []string,
	boardType ctdf.BoardType,
	directionWatermarks map[boardDirectionKey]time.Time,
) []*ctdf.DepartureBoard {
	filtered := make([]*ctdf.DepartureBoard, 0, len(entries))
	for _, entry := range entries {
		direction, ok := departureBoardDirection(entry, stopIDs, boardType)
		if ok {
			if watermark, found := directionWatermarks[direction]; found && entry.Time.Before(watermark) {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	return filtered
}
