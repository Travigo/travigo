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

const maximumRealtimePlanRecoveryAttempts = 3

func coordinateOriginStop(location *ctdf.Location) *ctdf.Stop {
	stop := &ctdf.Stop{PrimaryIdentifier: "coordinate-origin", PrimaryName: "Selected location", Active: true}
	if location == nil || len(location.Coordinates) != 2 {
		return stop
	}
	stop.PrimaryIdentifier = fmt.Sprintf("coordinate-origin:%.6f,%.6f", location.Coordinates[0], location.Coordinates[1])
	stop.Location = &ctdf.Location{Type: "Point", Coordinates: append([]float64(nil), location.Coordinates...)}
	return stop
}

func coordinateDestinationStop(location *ctdf.Location) *ctdf.Stop {
	stop := coordinateOriginStop(location)
	stop.PrimaryIdentifier = "coordinate-destination"
	stop.PrimaryName = "Selected destination"
	if location != nil && len(location.Coordinates) == 2 {
		stop.PrimaryIdentifier = fmt.Sprintf("coordinate-destination:%.6f,%.6f", location.Coordinates[0], location.Coordinates[1])
	}
	return stop
}

// JourneyPlanQuery is intentionally a thin service boundary. The journey graph
// owns topology and route calculation; web-api only hydrates the compact
// journey references required by the public CTDF response.
func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	if s.JourneyGraph == nil {
		return nil, fmt.Errorf("journey graph service is not configured")
	}
	if (q.DestinationStop == nil) == (q.DestinationLocation == nil) {
		return nil, fmt.Errorf("journey plan requires exactly one destination stop or location")
	}
	if (q.OriginStop == nil) == (q.OriginLocation == nil) {
		return nil, fmt.Errorf("journey plan requires exactly one origin stop or location")
	}

	requestedCount := q.Count
	if requestedCount <= 0 {
		requestedCount = 5
	}
	if requestedCount > 20 {
		requestedCount = 20
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
	request := departuregraph.PlanRequest{
		StartDateTime:             q.StartDateTime,
		Count:                     requestedCount,
		MaxChanges:                q.MaxChanges,
		MaxJourneyDurationSeconds: int(q.MaxJourneyDuration / time.Second),
		MaxTransferDistanceMetres: q.MaxTransferDistanceMetres,
		OriginLocationStopCount:   q.OriginLocationStopCount,
		MaxExpandedLabels:         q.MaxExpandedLabels,
		MaxSearchDurationMillis:   int(q.MaxSearchDuration / time.Millisecond),
		ExcludedJourneyRefs:       append([]string(nil), q.ExcludedJourneyRefs...),
	}
	if q.DestinationStop != nil {
		request.DestinationRefs = q.DestinationStop.GetAllStopIDs()
	} else if q.DestinationLocation != nil && len(q.DestinationLocation.Coordinates) == 2 {
		request.DestinationLocation = &departuregraph.PlanLocation{
			Longitude: q.DestinationLocation.Coordinates[0],
			Latitude:  q.DestinationLocation.Coordinates[1],
		}
	}
	if q.OriginStop != nil {
		request.OriginRefs = q.OriginStop.GetAllStopIDs()
	} else if q.OriginLocation != nil && len(q.OriginLocation.Coordinates) == 2 {
		request.OriginLocation = &departuregraph.PlanLocation{
			Longitude: q.OriginLocation.Coordinates[0],
			Latitude:  q.OriginLocation.Coordinates[1],
		}
	}
	var graphResult departuregraph.PlanResponse
	var err error
	if !q.ArrivalByDateTime.IsZero() {
		request.StartDateTime = q.ArrivalByDateTime.Add(-q.MaxJourneyDuration)
		if q.MaxJourneyDuration <= 0 {
			request.StartDateTime = q.ArrivalByDateTime.Add(-12 * time.Hour)
		}
		request.Count = 1
		arrivalPlan, arrivalErr := s.planArrivingBy(ctx, request, q.ArrivalByDateTime)
		if arrivalErr != nil {
			return nil, arrivalErr
		}
		request = arrivalPlan.Request
		graphResult = arrivalPlan.Response
	} else {
		graphResult, err = s.JourneyGraph.Plan(ctx, request)
	}
	if err != nil {
		return nil, err
	}
	journeys, err := hydrateGraphJourneys(ctx, graphResult)
	if err != nil {
		return nil, err
	}
	identifiers := graphJourneyIdentifiers(graphResult)
	staleReferences := len(journeys) != len(identifiers)
	cancelledJourneyIDs, err := ctdf.ActiveJourneyCancellationAlertIDs(ctx, identifiers, request.StartDateTime, time.Now())
	if err != nil {
		return nil, err
	}
	origin := q.OriginStop
	if origin == nil {
		origin = coordinateOriginStop(q.OriginLocation)
	}
	destination := q.DestinationStop
	if destination == nil {
		destination = coordinateDestinationStop(q.DestinationLocation)
	}
	result := &ctdf.JourneyPlanResults{
		JourneyPlans:          make([]ctdf.JourneyPlan, 0, len(graphResult.Plans)),
		OriginStop:            *origin,
		DestinationStop:       *destination,
		SearchTruncated:       graphResult.SearchTruncated,
		SearchTruncatedReason: graphResult.SearchTruncatedReason,
		ExpandedLabels:        graphResult.ExpandedLabels,
		SearchDurationMillis:  graphResult.SearchDurationMillis,
		FirstPlanMillis:       graphResult.FirstPlanMillis,
	}
	invalidCandidates := false
	invalidJourneyRefs := map[string]bool{}
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
					invalidJourneyRefs[leg.JourneyRef] = true
					valid = false
					break
				}
				if item.Journey.RealtimeJourney != nil && (item.Journey.RealtimeJourney.Cancelled || item.Journey.RealtimeJourney.SuppressesBoardAt(request.StartDateTime)) {
					invalidJourneyRefs[leg.JourneyRef] = true
					valid = false
					break
				}
				if _, cancelled := cancelledJourneyIDs[leg.JourneyRef]; cancelled {
					invalidJourneyRefs[leg.JourneyRef] = true
					valid = false
					break
				}
				if !applyRealtimeLegTimes(&item, leg.JourneyOriginStopIndex, leg.JourneyDestinationStopIndex) {
					invalidJourneyRefs[leg.JourneyRef] = true
					valid = false
					break
				}
				if !previousArrival.IsZero() && item.StartTime.Before(previousArrival) {
					invalidJourneyRefs[leg.JourneyRef] = true
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
		if len(result.JourneyPlans) == 0 && len(graphResult.Plans) > 0 && q.RecoveryAttempt >= maximumRealtimePlanRecoveryAttempts {
			return nil, fmt.Errorf("%w: every candidate plan references timetable journeys which no longer exist", departuregraph.ErrServiceUnavailable)
		}
		result.SearchTruncated = true
		result.SearchTruncatedReason = "stale_graph_references"
	} else if invalidCandidates {
		result.SearchTruncated = true
		result.SearchTruncatedReason = "post_hydration_filter"
	}
	if len(result.JourneyPlans) == 0 && len(invalidJourneyRefs) > 0 && q.RecoveryAttempt < maximumRealtimePlanRecoveryAttempts {
		retry := q
		retry.Context = ctx
		retry.RecoveryAttempt++
		seen := make(map[string]bool, len(q.ExcludedJourneyRefs)+len(invalidJourneyRefs))
		retry.ExcludedJourneyRefs = append([]string(nil), q.ExcludedJourneyRefs...)
		for _, ref := range retry.ExcludedJourneyRefs {
			seen[ref] = true
		}
		for ref := range invalidJourneyRefs {
			if !seen[ref] {
				retry.ExcludedJourneyRefs = append(retry.ExcludedJourneyRefs, ref)
			}
		}
		return s.JourneyPlanQuery(retry)
	}
	return result, nil
}

// planArrivingBy finds the latest usable departure by repeatedly asking the
// forward-only graph for its earliest arrival. Earliest arrival is monotonic
// as the permitted start time moves later, which lets this remain a small
// logarithmic number of normal graph searches without restoring the large
// reverse timetable index the graph intentionally no longer retains.
func (s Source) planArrivingBy(ctx context.Context, request departuregraph.PlanRequest, arrivalBy time.Time) (arrivalPlanResult, error) {
	if s.JourneyGraph == nil {
		return arrivalPlanResult{}, fmt.Errorf("journey graph service is not configured")
	}
	start := request.StartDateTime
	if start.After(arrivalBy) {
		return arrivalPlanResult{Request: request}, nil
	}
	latest := start.Add(-time.Minute)
	low, high := start, arrivalBy
	for low.Before(high) {
		midpoint := low.Add(high.Sub(low) / 2).Truncate(time.Minute)
		if midpoint.Before(low) {
			midpoint = low
		}
		probe := request
		probe.StartDateTime = midpoint
		probe.Count = 1
		response, err := s.JourneyGraph.Plan(ctx, probe)
		if err != nil {
			return arrivalPlanResult{}, err
		}
		if len(response.Plans) > 0 && !response.Plans[0].ArrivalTime.After(arrivalBy) {
			latest = midpoint
			low = midpoint.Add(time.Minute)
			continue
		}
		high = midpoint.Add(-time.Minute)
	}
	if !low.After(arrivalBy) {
		probe := request
		probe.StartDateTime = low
		probe.Count = 1
		response, err := s.JourneyGraph.Plan(ctx, probe)
		if err != nil {
			return arrivalPlanResult{}, err
		}
		if len(response.Plans) > 0 && !response.Plans[0].ArrivalTime.After(arrivalBy) {
			latest = low
		}
	}
	if latest.Before(start) {
		return arrivalPlanResult{Request: request}, nil
	}
	request.StartDateTime = latest
	request.Count = 1
	response, err := s.JourneyGraph.Plan(ctx, request)
	if err != nil {
		return arrivalPlanResult{}, err
	}
	response.Plans = filterPlansArrivingBy(response.Plans, arrivalBy)
	return arrivalPlanResult{Request: request, Response: response}, nil
}

type arrivalPlanResult struct {
	Request  departuregraph.PlanRequest
	Response departuregraph.PlanResponse
}

func filterPlansArrivingBy(plans []departuregraph.Plan, arrivalBy time.Time) []departuregraph.Plan {
	filtered := make([]departuregraph.Plan, 0, len(plans))
	for _, plan := range plans {
		if !plan.ArrivalTime.After(arrivalBy) {
			filtered = append(filtered, plan)
		}
	}
	return filtered
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
