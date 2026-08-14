package journeyplanner

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/departuregraph"
	"github.com/travigo/travigo/pkg/realtime/realtimestore"
	"go.mongodb.org/mongo-driver/bson"
)

func coordinateOriginStop(location *ctdf.Location) *ctdf.Stop {
	stop := &ctdf.Stop{PrimaryIdentifier: "coordinate-origin", PrimaryName: "Selected location", Active: true}
	if location == nil || len(location.Coordinates) != 2 {
		return stop
	}
	stop.PrimaryIdentifier = fmt.Sprintf("coordinate-origin:%.6f,%.6f", location.Coordinates[0], location.Coordinates[1])
	stop.Location = &ctdf.Location{Type: "Point", Coordinates: append([]float64(nil), location.Coordinates...)}
	return stop
}

// JourneyPlanQuery is intentionally a thin service boundary. The journey graph
// owns topology and route calculation; web-api only hydrates the compact
// journey references required by the public CTDF response.
func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	if s.JourneyGraph == nil {
		return nil, fmt.Errorf("journey graph service is not configured")
	}
	if q.DestinationStop == nil {
		return nil, fmt.Errorf("journey plan requires a destination stop")
	}
	if q.OriginStop == nil && q.OriginLocation == nil {
		return nil, fmt.Errorf("journey plan requires an origin stop or location")
	}

	requestedCount := q.Count
	if requestedCount <= 0 {
		requestedCount = 5
	}
	if requestedCount > 20 {
		requestedCount = 20
	}
	graphCount := requestedCount * 3
	if graphCount > 20 {
		graphCount = 20
	}
	request := departuregraph.PlanRequest{
		DestinationRefs:           q.DestinationStop.GetAllStopIDs(),
		StartDateTime:             q.StartDateTime,
		Count:                     graphCount,
		MaxChanges:                q.MaxChanges,
		MaxJourneyDurationSeconds: int(q.MaxJourneyDuration / time.Second),
		MaxTransferDistanceMetres: q.MaxTransferDistanceMetres,
		OriginLocationStopCount:   q.OriginLocationStopCount,
		MaxExpandedLabels:         q.MaxExpandedLabels,
		MaxSearchDurationMillis:   int(q.MaxSearchDuration / time.Millisecond),
	}
	if q.OriginStop != nil {
		request.OriginRefs = q.OriginStop.GetAllStopIDs()
	} else if q.OriginLocation != nil && len(q.OriginLocation.Coordinates) == 2 {
		request.OriginLocation = &departuregraph.PlanLocation{
			Longitude: q.OriginLocation.Coordinates[0],
			Latitude:  q.OriginLocation.Coordinates[1],
		}
	}

	ctx := q.Context
	if ctx == nil {
		ctx = context.Background()
	}
	searchTimeout := q.MaxSearchDuration + 10*time.Second
	if searchTimeout <= 10*time.Second {
		searchTimeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, searchTimeout)
	defer cancel()
	graphResult, err := s.JourneyGraph.Plan(ctx, request)
	if err != nil {
		return nil, err
	}
	journeys, err := hydrateGraphJourneys(ctx, graphResult)
	if err != nil {
		return nil, err
	}
	identifiers := graphJourneyIdentifiers(graphResult)
	staleReferences := len(journeys) != len(identifiers)
	cancelledJourneyIDs, err := ctdf.ActiveJourneyCancellationAlertIDs(ctx, identifiers, q.StartDateTime, time.Now())
	if err != nil {
		return nil, err
	}
	origin := q.OriginStop
	if origin == nil {
		origin = coordinateOriginStop(q.OriginLocation)
	}
	result := &ctdf.JourneyPlanResults{
		JourneyPlans:          make([]ctdf.JourneyPlan, 0, len(graphResult.Plans)),
		OriginStop:            *origin,
		DestinationStop:       *q.DestinationStop,
		SearchTruncated:       graphResult.SearchTruncated,
		SearchTruncatedReason: graphResult.SearchTruncatedReason,
	}
	invalidCandidates := false
	for _, graphPlan := range graphResult.Plans {
		plan := ctdf.JourneyPlan{
			RouteItems:  make([]ctdf.JourneyPlanRouteItem, 0, len(graphPlan.Legs)),
			StartTime:   graphPlan.StartTime,
			ArrivalTime: graphPlan.ArrivalTime,
			Duration:    graphPlan.ArrivalTime.Sub(graphPlan.StartTime),
		}
		valid := true
		var previousArrival time.Time
		for _, leg := range graphPlan.Legs {
			item := ctdf.JourneyPlanRouteItem{
				Type:                     leg.Type,
				TransferType:             leg.TransferType,
				OriginStopRef:            leg.OriginStopRef,
				DestinationStopRef:       leg.DestinationStopRef,
				StartTime:                leg.StartTime,
				ArrivalTime:              leg.ArrivalTime,
				DistanceMetres:           leg.DistanceMetres,
				WalkDurationSeconds:      leg.WalkDurationSeconds,
				MinChangeDurationSeconds: leg.MinChangeDurationSeconds,
				TotalDurationSeconds:     leg.TotalDurationSeconds,
			}
			if leg.JourneyRef != "" {
				item.Journey = journeys[leg.JourneyRef]
				item.JourneyType = ctdf.DepartureBoardRecordTypeScheduled
				if item.Journey == nil {
					valid = false
					break
				}
				if item.Journey.RealtimeJourney != nil && (item.Journey.RealtimeJourney.Cancelled || item.Journey.RealtimeJourney.SuppressesBoardAt(q.StartDateTime)) {
					valid = false
					break
				}
				if _, cancelled := cancelledJourneyIDs[leg.JourneyRef]; cancelled {
					valid = false
					break
				}
				if !applyRealtimeLegTimes(&item, leg.JourneyOriginStopIndex, leg.JourneyDestinationStopIndex) {
					valid = false
					break
				}
				if !previousArrival.IsZero() && item.StartTime.Before(previousArrival) {
					valid = false
					break
				}
			} else if !previousArrival.IsZero() {
				item.StartTime = previousArrival
				item.ArrivalTime = item.StartTime.Add(time.Duration(item.TotalDurationSeconds) * time.Second)
			}
			plan.RouteItems = append(plan.RouteItems, item)
			previousArrival = item.ArrivalTime
		}
		if valid {
			if len(plan.RouteItems) > 0 {
				plan.StartTime = plan.RouteItems[0].StartTime
				plan.ArrivalTime = plan.RouteItems[len(plan.RouteItems)-1].ArrivalTime
				plan.Duration = plan.ArrivalTime.Sub(plan.StartTime)
			}
			result.JourneyPlans = append(result.JourneyPlans, plan)
			if len(result.JourneyPlans) >= requestedCount {
				break
			}
		} else {
			invalidCandidates = true
		}
	}
	sort.Slice(result.JourneyPlans, func(i, j int) bool {
		if result.JourneyPlans[i].ArrivalTime.Equal(result.JourneyPlans[j].ArrivalTime) {
			return result.JourneyPlans[i].StartTime.Before(result.JourneyPlans[j].StartTime)
		}
		return result.JourneyPlans[i].ArrivalTime.Before(result.JourneyPlans[j].ArrivalTime)
	})
	if len(result.JourneyPlans) >= requestedCount {
		result.SearchTruncated = false
		result.SearchTruncatedReason = ""
	} else if staleReferences {
		if len(result.JourneyPlans) == 0 && len(graphResult.Plans) > 0 {
			return nil, fmt.Errorf("%w: every candidate plan references timetable journeys which no longer exist", departuregraph.ErrServiceUnavailable)
		}
		result.SearchTruncated = true
		result.SearchTruncatedReason = "stale_graph_references"
	} else if invalidCandidates {
		result.SearchTruncated = true
		result.SearchTruncatedReason = "post_hydration_filter"
	}
	return result, nil
}

func graphJourneyIdentifiers(result departuregraph.PlanResponse) []string {
	identifiers := make([]string, 0)
	seen := map[string]bool{}
	for _, plan := range result.Plans {
		for _, leg := range plan.Legs {
			if leg.JourneyRef != "" && !seen[leg.JourneyRef] {
				seen[leg.JourneyRef] = true
				identifiers = append(identifiers, leg.JourneyRef)
			}
		}
	}
	return identifiers
}

func hydrateGraphJourneys(ctx context.Context, result departuregraph.PlanResponse) (map[string]*ctdf.Journey, error) {
	identifiers := graphJourneyIdentifiers(result)
	journeys := make(map[string]*ctdf.Journey, len(identifiers))
	if len(identifiers) == 0 {
		return journeys, nil
	}
	cursor, err := database.GetCollection(database.JourneysCollectionName).Find(ctx, bson.M{
		"primaryidentifier": bson.M{"$in": identifiers},
	})
	if err != nil {
		return nil, err
	}
	for cursor.Next(ctx) {
		var journey ctdf.Journey
		if err := cursor.Decode(&journey); err != nil {
			return nil, err
		}
		journeys[journey.PrimaryIdentifier] = &journey
	}
	if err := cursor.Err(); err != nil {
		_ = cursor.Close(ctx)
		return nil, err
	}
	_ = cursor.Close(ctx)
	realtime, err := realtimestore.FindCurrentForJourneyIDs(ctx, identifiers)
	if err == nil {
		for identifier, realtimeJourney := range realtime {
			if journeys[identifier] != nil {
				journeys[identifier].RealtimeJourney = realtimeJourney
			}
		}
	}
	return journeys, nil
}

func applyRealtimeLegTimes(item *ctdf.JourneyPlanRouteItem, boardingIndex, destinationIndex int) bool {
	if item == nil || item.Journey == nil || item.Journey.RealtimeJourney == nil {
		return true
	}
	journey := item.Journey
	if boardingIndex < 0 || boardingIndex >= len(journey.Path) || destinationIndex <= boardingIndex || destinationIndex > len(journey.Path) || journey.Path[boardingIndex] == nil || journey.Path[boardingIndex].OriginStopRef != item.OriginStopRef || journey.Path[destinationIndex-1] == nil || journey.Path[destinationIndex-1].DestinationStopRef != item.DestinationStopRef {
		boardingIndex, destinationIndex = graphLegStopIndexes(journey, item)
	}
	if boardingIndex < 0 || destinationIndex < 0 {
		return false
	}
	if stop := journey.RealtimeJourney.RealtimeStop(item.OriginStopRef, boardingIndex); stop != nil {
		if stop.Cancelled {
			return false
		}
		if !stop.DepartureTime.IsZero() {
			item.StartTime = realtimeTimeOnOccurrence(item.StartTime, stop.DepartureTime)
		}
	}
	if stop := journey.RealtimeJourney.RealtimeStop(item.DestinationStopRef, destinationIndex); stop != nil {
		if stop.Cancelled {
			return false
		}
		if !stop.ArrivalTime.IsZero() {
			item.ArrivalTime = realtimeTimeOnOccurrence(item.ArrivalTime, stop.ArrivalTime)
		}
	}
	return !item.ArrivalTime.Before(item.StartTime)
}

func graphLegStopIndexes(journey *ctdf.Journey, item *ctdf.JourneyPlanRouteItem) (int, int) {
	boardingIndex := -1
	destinationIndex := -1
	for index, path := range journey.Path {
		if path == nil {
			continue
		}
		if boardingIndex < 0 && path.OriginStopRef == item.OriginStopRef {
			boardingIndex = index
		}
		if boardingIndex >= 0 && path.DestinationStopRef == item.DestinationStopRef {
			destinationIndex = index + 1
			break
		}
	}
	return boardingIndex, destinationIndex
}

func realtimeTimeOnOccurrence(scheduled, realtime time.Time) time.Time {
	if realtime.Year() > 1 {
		return realtime
	}
	start := time.Date(0, time.January, 1, 0, 0, 0, 0, realtime.Location())
	offset := realtime.Sub(start)
	midnight := time.Date(scheduled.Year(), scheduled.Month(), scheduled.Day(), 0, 0, 0, 0, scheduled.Location())
	return midnight.Add(offset)
}
