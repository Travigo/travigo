package journeyplanner

import (
	"container/heap"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultJourneyPlanCount                   = 25
	defaultJourneyPlanMaxChanges              = 3
	defaultJourneyPlanMaxJourneyDuration      = 6 * time.Hour
	defaultJourneyPlanMaxTransferDistance     = 1000
	defaultJourneyPlanDepartureBoardCount     = 40
	defaultJourneyPlanMaxExpandedLabels       = 500
	defaultJourneyPlanMaxRouteItems           = 10
	defaultJourneyPlanMaxConsecutiveTransfers = 1
)

type plannerConfig struct {
	count                   int
	maxVehicleLegs          int
	maxJourneyDuration      time.Duration
	maxTransferDistance     int
	departureBoardCount     int
	maxExpandedLabels       int
	maxRouteItems           int
	maxConsecutiveTransfers int
	maxLabelsPerState       int
}

type plannerStateKey struct {
	stopRef              string
	vehicleLegs          int
	consecutiveTransfers int
}

type plannerLabel struct {
	stop                 *ctdf.Stop
	arrivalTime          time.Time
	vehicleLegs          int
	consecutiveTransfers int
	routeItems           []ctdf.JourneyPlanRouteItem
	index                int
}

type plannerPriorityQueue []*plannerLabel

func (pq plannerPriorityQueue) Len() int {
	return len(pq)
}

func (pq plannerPriorityQueue) Less(i, j int) bool {
	if pq[i].arrivalTime.Equal(pq[j].arrivalTime) {
		return pq[i].vehicleLegs < pq[j].vehicleLegs
	}
	return pq[i].arrivalTime.Before(pq[j].arrivalTime)
}

func (pq plannerPriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *plannerPriorityQueue) Push(x any) {
	item := x.(*plannerLabel)
	item.index = len(*pq)
	*pq = append(*pq, item)
}

func (pq *plannerPriorityQueue) Pop() any {
	old := *pq
	n := len(old)
	item := old[n-1]
	old[n-1] = nil
	item.index = -1
	*pq = old[0 : n-1]
	return item
}

type plannerRuntime struct {
	stopCache     map[string]*ctdf.Stop
	transferCache map[string][]*ctdf.StopTransfer
	bestArrivals  map[plannerStateKey][]time.Time
	resultKeys    map[string]bool
	config        plannerConfig
	searchEndTime time.Time
}

func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	if q.OriginStop == nil || q.DestinationStop == nil {
		return nil, fmt.Errorf("journey plan requires origin and destination stops")
	}

	config := journeyPlanConfig(q)
	runtime := &plannerRuntime{
		stopCache:     map[string]*ctdf.Stop{},
		transferCache: map[string][]*ctdf.StopTransfer{},
		bestArrivals:  map[plannerStateKey][]time.Time{},
		resultKeys:    map[string]bool{},
		config:        config,
		searchEndTime: q.StartDateTime.Add(config.maxJourneyDuration),
	}
	runtime.cacheStop(q.OriginStop)
	runtime.cacheStop(q.DestinationStop)

	results := &ctdf.JourneyPlanResults{
		JourneyPlans:    []ctdf.JourneyPlan{},
		OriginStop:      *q.OriginStop,
		DestinationStop: *q.DestinationStop,
	}

	initialLabel := &plannerLabel{
		stop:        q.OriginStop,
		arrivalTime: q.StartDateTime,
		routeItems:  []ctdf.JourneyPlanRouteItem{},
	}

	pq := &plannerPriorityQueue{}
	heap.Init(pq)
	runtime.pushLabel(pq, initialLabel)

	expandedLabels := 0
	for pq.Len() > 0 && len(results.JourneyPlans) < config.count && expandedLabels < config.maxExpandedLabels {
		current := heap.Pop(pq).(*plannerLabel)
		expandedLabels++

		if stopMatchesStop(current.stop, q.DestinationStop) && len(current.routeItems) > 0 {
			plan := buildJourneyPlan(current)
			key := journeyPlanKey(plan)
			if !runtime.resultKeys[key] {
				results.JourneyPlans = append(results.JourneyPlans, plan)
				runtime.resultKeys[key] = true
			}
			continue
		}

		if len(current.routeItems) >= config.maxRouteItems || current.arrivalTime.After(runtime.searchEndTime) {
			continue
		}

		if current.consecutiveTransfers < config.maxConsecutiveTransfers {
			if err := runtime.expandTransfers(pq, current); err != nil {
				return nil, err
			}
		}

		if current.vehicleLegs < config.maxVehicleLegs {
			if err := runtime.expandDepartures(pq, current); err != nil {
				return nil, err
			}
		}
	}

	sort.Slice(results.JourneyPlans, func(i, j int) bool {
		if results.JourneyPlans[i].StartTime.Equal(results.JourneyPlans[j].StartTime) {
			return results.JourneyPlans[i].ArrivalTime.Before(results.JourneyPlans[j].ArrivalTime)
		}
		return results.JourneyPlans[i].StartTime.Before(results.JourneyPlans[j].StartTime)
	})

	if len(results.JourneyPlans) > config.count {
		results.JourneyPlans = results.JourneyPlans[:config.count]
	}

	return results, nil
}

func journeyPlanConfig(q query.JourneyPlan) plannerConfig {
	count := q.Count
	if count <= 0 {
		count = defaultJourneyPlanCount
	}

	maxChanges := q.MaxChanges
	if maxChanges < 0 || (maxChanges == 0 && q.MaxJourneyDuration <= 0 && q.MaxTransferDistanceMetres <= 0 && q.DepartureBoardCountPerStop <= 0) {
		maxChanges = defaultJourneyPlanMaxChanges
	}

	maxJourneyDuration := q.MaxJourneyDuration
	if maxJourneyDuration <= 0 {
		maxJourneyDuration = defaultJourneyPlanMaxJourneyDuration
	}

	maxTransferDistance := q.MaxTransferDistanceMetres
	if maxTransferDistance <= 0 {
		maxTransferDistance = defaultJourneyPlanMaxTransferDistance
	}

	departureBoardCount := q.DepartureBoardCountPerStop
	if departureBoardCount <= 0 {
		departureBoardCount = defaultJourneyPlanDepartureBoardCount
	}

	return plannerConfig{
		count:                   count,
		maxVehicleLegs:          maxChanges + 1,
		maxJourneyDuration:      maxJourneyDuration,
		maxTransferDistance:     maxTransferDistance,
		departureBoardCount:     departureBoardCount,
		maxExpandedLabels:       defaultJourneyPlanMaxExpandedLabels,
		maxRouteItems:           defaultJourneyPlanMaxRouteItems,
		maxConsecutiveTransfers: defaultJourneyPlanMaxConsecutiveTransfers,
		maxLabelsPerState:       count,
	}
}

func (runtime *plannerRuntime) expandTransfers(pq *plannerPriorityQueue, current *plannerLabel) error {
	transfers, err := runtime.loadTransfers(current.stop)
	if err != nil {
		return err
	}

	for _, transfer := range transfers {
		if transfer == nil || transfer.ToStopRef == "" {
			continue
		}
		if transfer.DistanceMetres > runtime.config.maxTransferDistance {
			continue
		}

		totalDurationSeconds := transfer.TotalDurationSeconds
		if totalDurationSeconds <= 0 {
			totalDurationSeconds = transfer.WalkDurationSeconds + transfer.MinChangeDurationSeconds
		}
		if totalDurationSeconds <= 0 {
			continue
		}

		arrivalTime := current.arrivalTime.Add(time.Duration(totalDurationSeconds) * time.Second)
		if arrivalTime.After(runtime.searchEndTime) {
			continue
		}

		toStop, err := runtime.lookupStop(transfer.ToStopRef)
		if err != nil || toStop == nil {
			continue
		}

		routeItem := ctdf.JourneyPlanRouteItem{
			Type:                     ctdf.JourneyPlanRouteItemTypeTransfer,
			TransferType:             transfer.Type,
			OriginStopRef:            transfer.FromStopRef,
			DestinationStopRef:       transfer.ToStopRef,
			StartTime:                current.arrivalTime,
			ArrivalTime:              arrivalTime,
			DistanceMetres:           transfer.DistanceMetres,
			WalkDurationSeconds:      transfer.WalkDurationSeconds,
			MinChangeDurationSeconds: transfer.MinChangeDurationSeconds,
			TotalDurationSeconds:     totalDurationSeconds,
		}

		runtime.pushLabel(pq, &plannerLabel{
			stop:                 toStop,
			arrivalTime:          arrivalTime,
			vehicleLegs:          current.vehicleLegs,
			consecutiveTransfers: current.consecutiveTransfers + 1,
			routeItems:           appendRouteItem(current.routeItems, routeItem),
		})
	}

	return nil
}

func (runtime *plannerRuntime) expandDepartures(pq *plannerPriorityQueue, current *plannerLabel) error {
	departureBoard, err := dataaggregator.Lookup[[]*ctdf.DepartureBoard](query.DepartureBoard{
		Stop:          current.stop,
		Count:         runtime.config.departureBoardCount,
		StartDateTime: current.arrivalTime,
	})
	if err != nil {
		return err
	}

	sort.Slice(departureBoard, func(i, j int) bool {
		return departureBoard[i].Time.Before(departureBoard[j].Time)
	})

	for _, departure := range departureBoard {
		if departure == nil || departure.Journey == nil || len(departure.Journey.Path) == 0 {
			continue
		}
		if departure.Type == ctdf.DepartureBoardRecordTypeCancelled {
			continue
		}
		if departure.Time.Before(current.arrivalTime) || departure.Time.After(runtime.searchEndTime) {
			continue
		}

		boardingIndex := boardingPathIndex(departure.Journey, current.stop)
		if boardingIndex < 0 {
			continue
		}

		boardingStopRef := departure.Journey.Path[boardingIndex].OriginStopRef
		lastTime := departure.Time
		for pathIndex := boardingIndex; pathIndex < len(departure.Journey.Path); pathIndex++ {
			pathItem := departure.Journey.Path[pathIndex]
			if pathItem == nil || pathItem.DestinationStopRef == "" {
				continue
			}

			arrivalTime := pathTimeOnOrAfter(lastTime, pathItem.DestinationArrivalTime)
			lastTime = arrivalTime

			if arrivalTime.After(runtime.searchEndTime) {
				break
			}

			toStop, err := runtime.lookupStop(pathItem.DestinationStopRef)
			if err != nil || toStop == nil {
				continue
			}

			routeItem := ctdf.JourneyPlanRouteItem{
				Type:               ctdf.JourneyPlanRouteItemTypeJourney,
				Journey:            departure.Journey,
				JourneyType:        departure.Type,
				OriginStopRef:      boardingStopRef,
				DestinationStopRef: pathItem.DestinationStopRef,
				StartTime:          departure.Time,
				ArrivalTime:        arrivalTime,
			}

			runtime.pushLabel(pq, &plannerLabel{
				stop:        toStop,
				arrivalTime: arrivalTime,
				vehicleLegs: current.vehicleLegs + 1,
				routeItems:  appendRouteItem(current.routeItems, routeItem),
			})
		}
	}

	return nil
}

func (runtime *plannerRuntime) loadTransfers(stop *ctdf.Stop) ([]*ctdf.StopTransfer, error) {
	if stop == nil {
		return nil, nil
	}

	if transfers, exists := runtime.transferCache[stop.PrimaryIdentifier]; exists {
		return transfers, nil
	}

	stopTransfersCollection := database.GetCollection("stop_transfers")
	opts := options.Find().SetProjection(bson.D{
		bson.E{Key: "_id", Value: 0},
	})
	cursor, err := stopTransfersCollection.Find(context.Background(), bson.M{
		"fromstopref": bson.M{"$in": stop.GetAllStopIDs()},
	}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	transfers := make([]*ctdf.StopTransfer, 0, 8)
	for cursor.Next(context.Background()) {
		var transfer ctdf.StopTransfer
		if err := cursor.Decode(&transfer); err != nil {
			return nil, err
		}
		transfers = append(transfers, &transfer)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	runtime.transferCache[stop.PrimaryIdentifier] = transfers
	return transfers, nil
}

func (runtime *plannerRuntime) lookupStop(identifier string) (*ctdf.Stop, error) {
	if stop, exists := runtime.stopCache[identifier]; exists {
		return stop, nil
	}

	stop, err := dataaggregator.Lookup[*ctdf.Stop](query.Stop{
		Identifier: identifier,
	})
	if err != nil {
		return nil, err
	}

	runtime.cacheStop(stop)
	runtime.stopCache[identifier] = stop
	return stop, nil
}

func (runtime *plannerRuntime) cacheStop(stop *ctdf.Stop) {
	if stop == nil {
		return
	}

	for _, stopID := range stop.GetAllStopIDs() {
		if stopID == "" {
			continue
		}
		runtime.stopCache[stopID] = stop
	}
}

func (runtime *plannerRuntime) pushLabel(pq *plannerPriorityQueue, label *plannerLabel) {
	if label == nil || label.stop == nil || label.arrivalTime.After(runtime.searchEndTime) {
		return
	}

	key := plannerStateKey{
		stopRef:              label.stop.PrimaryIdentifier,
		vehicleLegs:          label.vehicleLegs,
		consecutiveTransfers: label.consecutiveTransfers,
	}

	arrivals := runtime.bestArrivals[key]
	for _, arrival := range arrivals {
		if arrival.Equal(label.arrivalTime) {
			return
		}
	}

	if len(arrivals) >= runtime.config.maxLabelsPerState {
		sort.Slice(arrivals, func(i, j int) bool {
			return arrivals[i].Before(arrivals[j])
		})

		lastIndex := len(arrivals) - 1
		if !label.arrivalTime.Before(arrivals[lastIndex]) {
			return
		}

		arrivals[lastIndex] = label.arrivalTime
	} else {
		arrivals = append(arrivals, label.arrivalTime)
	}

	sort.Slice(arrivals, func(i, j int) bool {
		return arrivals[i].Before(arrivals[j])
	})

	runtime.bestArrivals[key] = arrivals
	heap.Push(pq, label)
}

func buildJourneyPlan(label *plannerLabel) ctdf.JourneyPlan {
	startTime := label.arrivalTime
	if len(label.routeItems) > 0 {
		startTime = label.routeItems[0].StartTime
	}

	return ctdf.JourneyPlan{
		RouteItems:  label.routeItems,
		StartTime:   startTime,
		ArrivalTime: label.arrivalTime,
		Duration:    label.arrivalTime.Sub(startTime),
	}
}

func journeyPlanKey(plan ctdf.JourneyPlan) string {
	parts := make([]string, 0, len(plan.RouteItems))
	for _, item := range plan.RouteItems {
		journeyRef := ""
		if item.Journey != nil {
			journeyRef = item.Journey.PrimaryIdentifier
		}

		parts = append(parts, fmt.Sprintf(
			"%s:%s:%s:%s:%d:%d",
			item.Type,
			journeyRef,
			item.OriginStopRef,
			item.DestinationStopRef,
			item.StartTime.Unix(),
			item.ArrivalTime.Unix(),
		))
	}

	return strings.Join(parts, "|")
}

func appendRouteItem(routeItems []ctdf.JourneyPlanRouteItem, routeItem ctdf.JourneyPlanRouteItem) []ctdf.JourneyPlanRouteItem {
	nextRouteItems := make([]ctdf.JourneyPlanRouteItem, 0, len(routeItems)+1)
	nextRouteItems = append(nextRouteItems, routeItems...)
	nextRouteItems = append(nextRouteItems, routeItem)
	return nextRouteItems
}

func boardingPathIndex(journey *ctdf.Journey, stop *ctdf.Stop) int {
	for index, pathItem := range journey.Path {
		if pathItem == nil {
			continue
		}
		if stopMatchesRef(stop, pathItem.OriginStopRef) {
			return index
		}
	}

	return -1
}

func pathTimeOnOrAfter(referenceTime time.Time, pathTime time.Time) time.Time {
	dateTime := time.Date(
		referenceTime.Year(),
		referenceTime.Month(),
		referenceTime.Day(),
		pathTime.Hour(),
		pathTime.Minute(),
		pathTime.Second(),
		pathTime.Nanosecond(),
		referenceTime.Location(),
	)

	for dateTime.Before(referenceTime) {
		dateTime = dateTime.Add(24 * time.Hour)
	}

	return dateTime
}

func stopMatchesStop(a *ctdf.Stop, b *ctdf.Stop) bool {
	if a == nil || b == nil {
		return false
	}

	for _, stopID := range a.GetAllStopIDs() {
		if stopMatchesRef(b, stopID) {
			return true
		}
	}

	return false
}

func stopMatchesRef(stop *ctdf.Stop, stopRef string) bool {
	if stop == nil || stopRef == "" {
		return false
	}

	for _, stopID := range stop.GetAllStopIDs() {
		if stopID == stopRef {
			return true
		}
	}

	return false
}
