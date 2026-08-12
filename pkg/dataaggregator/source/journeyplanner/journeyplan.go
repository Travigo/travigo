package journeyplanner

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
	"github.com/travigo/travigo/pkg/database"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	defaultJourneyPlanCount                       = 5
	defaultJourneyPlanMaxChanges                  = 3
	defaultJourneyPlanMaxJourneyDuration          = 6 * time.Hour
	defaultJourneyPlanMaxTransferDistance         = 1000
	defaultJourneyPlanDepartureBoardCount         = 12
	defaultJourneyPlanOriginDepartureBoardCount   = 24
	defaultJourneyPlanOriginLocationStopCount     = 12
	defaultJourneyPlanMaxExpandedLabels           = 500
	defaultJourneyPlanMaxRouteItems               = 10
	defaultJourneyPlanMaxConsecutiveTransfers     = 1
	defaultJourneyPlanMaxSearchDuration           = 16 * time.Second
	defaultJourneyPlanWalkSpeedMetresPerSecond    = 1.3
	defaultJourneyPlanDestinationApproachMetres   = 15000
	defaultJourneyPlanAccessVehicleLegs           = 1
	defaultJourneyPlanMaximumSpeedMetresPerSecond = 100
)

type plannerConfig struct {
	count                     int
	maxVehicleLegs            int
	maxJourneyDuration        time.Duration
	maxTransferDistance       int
	departureBoardCount       int
	originDepartureBoardCount int
	originLocationStopCount   int
	maxExpandedLabels         int
	maxRouteItems             int
	maxConsecutiveTransfers   int
	maxLabelsPerState         int
	maxSearchDuration         time.Duration
}

type plannerStateKey struct {
	stopRef              string
	vehicleLegs          int
	consecutiveTransfers int
}

// routeItemNode is an immutable node in a persistent singly-linked list of
// route items. Sharing the tail between labels means expanding a label is O(1)
// instead of copying the whole route on every push.
type routeItemNode struct {
	item   ctdf.JourneyPlanRouteItem
	parent *routeItemNode
	depth  int
}

type plannerLabel struct {
	stop                 *ctdf.Stop
	arrivalTime          time.Time
	vehicleLegs          int
	consecutiveTransfers int
	departuresExpanded   bool
	transfersExpanded    bool
	routeItems           *routeItemNode
	preferExpansion      bool
	priorityClass        uint8
	priorityTime         time.Time
	index                int
}

type departureCandidate struct {
	departure          *ctdf.DepartureBoard
	boardingIndex      int
	reachesDestination bool
}

type plannerPriorityQueue []*plannerLabel

func (pq plannerPriorityQueue) Len() int {
	return len(pq)
}

func (pq plannerPriorityQueue) Less(i, j int) bool {
	if pq[i].priorityClass != pq[j].priorityClass {
		return pq[i].priorityClass < pq[j].priorityClass
	}
	iPriority := plannerLabelPriority(pq[i])
	jPriority := plannerLabelPriority(pq[j])
	if iPriority.Equal(jPriority) {
		if !pq[i].arrivalTime.Equal(pq[j].arrivalTime) {
			return pq[i].arrivalTime.Before(pq[j].arrivalTime)
		}
		return pq[i].vehicleLegs < pq[j].vehicleLegs
	}
	return iPriority.Before(jPriority)
}

func plannerLabelPriority(label *plannerLabel) time.Time {
	if label == nil {
		return time.Time{}
	}
	if label.priorityTime.IsZero() {
		return label.arrivalTime
	}
	return label.priorityTime
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

type cachedDepartureBoard struct {
	fetchTime time.Time
	count     int
	board     []*ctdf.DepartureBoard
}

type plannerRuntime struct {
	stopCache            map[string]*ctdf.Stop
	transferCache        map[string][]*ctdf.StopTransfer
	departureBoardCache  map[string]cachedDepartureBoard
	departureBoardLookup func(query.DepartureBoard) ([]*ctdf.DepartureBoard, error)
	bestArrivals         map[plannerStateKey][]time.Time
	resultKeys           map[string]bool
	config               plannerConfig
	searchEndTime        time.Time
	searchDeadline       time.Time
	destinationStop      *ctdf.Stop
	timedOut             bool
}

func (s Source) JourneyPlanQuery(q query.JourneyPlan) (*ctdf.JourneyPlanResults, error) {
	if q.DestinationStop == nil {
		return nil, fmt.Errorf("journey plan requires a destination stop")
	}
	if q.OriginStop == nil && q.OriginLocation == nil {
		return nil, fmt.Errorf("journey plan requires an origin stop or origin location")
	}

	config := journeyPlanConfig(q)
	originStop := q.OriginStop
	if originStop == nil {
		originStop = coordinateOriginStop(q.OriginLocation)
	}

	runtime := &plannerRuntime{
		stopCache:           map[string]*ctdf.Stop{},
		transferCache:       map[string][]*ctdf.StopTransfer{},
		departureBoardCache: map[string]cachedDepartureBoard{},
		bestArrivals:        map[plannerStateKey][]time.Time{},
		resultKeys:          map[string]bool{},
		config:              config,
		searchEndTime:       q.StartDateTime.Add(config.maxJourneyDuration),
		searchDeadline:      time.Now().Add(config.maxSearchDuration),
		destinationStop:     q.DestinationStop,
	}
	runtime.cacheStop(originStop)
	runtime.cacheStop(q.DestinationStop)

	results := &ctdf.JourneyPlanResults{
		JourneyPlans:    []ctdf.JourneyPlan{},
		OriginStop:      *originStop,
		DestinationStop: *q.DestinationStop,
	}

	pq := &plannerPriorityQueue{}
	heap.Init(pq)

	if q.OriginStop != nil {
		runtime.pushLabel(pq, &plannerLabel{
			stop:            q.OriginStop,
			arrivalTime:     q.StartDateTime,
			preferExpansion: true,
		})
	} else if err := runtime.pushOriginLocationLabels(pq, originStop, q.OriginLocation, q.StartDateTime); err != nil {
		return nil, err
	}

	expandedLabels := 0
	searchStart := time.Now()
	for pq.Len() > 0 && len(results.JourneyPlans) < config.count && expandedLabels < config.maxExpandedLabels && !runtime.searchExpired() {
		current := heap.Pop(pq).(*plannerLabel)
		if !runtime.isCurrentLabel(current) {
			continue
		}
		expandedLabels++

		if stopMatchesStop(current.stop, q.DestinationStop) && current.routeItems != nil {
			runtime.recordResult(results, current)
			continue
		}

		if routeItemDepth(current.routeItems) >= config.maxRouteItems || current.arrivalTime.After(runtime.searchEndTime) {
			continue
		}

		if !current.departuresExpanded && current.vehicleLegs < config.maxVehicleLegs {
			if err := runtime.expandDepartures(pq, current, q.DestinationStop, results); err != nil {
				return nil, err
			}
		}
		if len(results.JourneyPlans) >= config.count {
			continue
		}

		if !current.transfersExpanded && current.consecutiveTransfers < config.maxConsecutiveTransfers {
			if err := runtime.expandTransfers(pq, current, q.DestinationStop, results); err != nil {
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

	if runtime.timedOut {
		results.SearchTruncated = true
		results.SearchTruncatedReason = "time_budget"
		log.Warn().
			Int("results", len(results.JourneyPlans)).
			Int("expanded_labels", expandedLabels).
			Dur("duration", time.Since(searchStart)).
			Dur("max_search_duration", config.maxSearchDuration).
			Msg("Journey planner search stopped by time budget")
	} else if expandedLabels >= config.maxExpandedLabels && pq.Len() > 0 && len(results.JourneyPlans) < config.count {
		results.SearchTruncated = true
		results.SearchTruncatedReason = "expanded_label_budget"
		log.Warn().
			Int("results", len(results.JourneyPlans)).
			Int("expanded_labels", expandedLabels).
			Int("max_expanded_labels", config.maxExpandedLabels).
			Dur("duration", time.Since(searchStart)).
			Msg("Journey planner search stopped by expanded label budget")
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

	originDepartureBoardCount := q.OriginDepartureBoardCount
	if originDepartureBoardCount <= 0 {
		originDepartureBoardCount = defaultJourneyPlanOriginDepartureBoardCount
	}
	if originDepartureBoardCount < departureBoardCount {
		originDepartureBoardCount = departureBoardCount
	}

	originLocationStopCount := q.OriginLocationStopCount
	if originLocationStopCount <= 0 {
		originLocationStopCount = defaultJourneyPlanOriginLocationStopCount
	}

	maxExpandedLabels := q.MaxExpandedLabels
	if maxExpandedLabels <= 0 {
		maxExpandedLabels = defaultJourneyPlanMaxExpandedLabels
	}

	maxSearchDuration := q.MaxSearchDuration
	if maxSearchDuration <= 0 {
		maxSearchDuration = defaultJourneyPlanMaxSearchDuration
	}

	return plannerConfig{
		count:                     count,
		maxVehicleLegs:            maxChanges + 1,
		maxJourneyDuration:        maxJourneyDuration,
		maxTransferDistance:       maxTransferDistance,
		departureBoardCount:       departureBoardCount,
		originDepartureBoardCount: originDepartureBoardCount,
		originLocationStopCount:   originLocationStopCount,
		maxExpandedLabels:         maxExpandedLabels,
		maxRouteItems:             defaultJourneyPlanMaxRouteItems,
		maxConsecutiveTransfers:   defaultJourneyPlanMaxConsecutiveTransfers,
		maxLabelsPerState:         count,
		maxSearchDuration:         maxSearchDuration,
	}
}

func coordinateOriginStop(location *ctdf.Location) *ctdf.Stop {
	if location == nil || len(location.Coordinates) != 2 {
		return &ctdf.Stop{
			PrimaryIdentifier: "coordinate-origin",
			PrimaryName:       "Selected location",
			Active:            true,
		}
	}

	coordinates := []float64{location.Coordinates[0], location.Coordinates[1]}
	return &ctdf.Stop{
		PrimaryIdentifier: fmt.Sprintf("coordinate-origin:%.6f,%.6f", coordinates[0], coordinates[1]),
		PrimaryName:       "Selected location",
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: coordinates,
		},
		Active: true,
	}
}

func (runtime *plannerRuntime) pushOriginLocationLabels(pq *plannerPriorityQueue, originStop *ctdf.Stop, originLocation *ctdf.Location, startDateTime time.Time) error {
	nearbyStops, err := runtime.loadOriginLocationStops(originLocation)
	if err != nil {
		return err
	}

	for _, stop := range nearbyStops {
		label := originLocationLabel(originStop, originLocation, stop, startDateTime)
		runtime.pushLabel(pq, label)
	}

	return nil
}

func originLocationLabel(originStop *ctdf.Stop, originLocation *ctdf.Location, stop *ctdf.Stop, startDateTime time.Time) *plannerLabel {
	if originStop == nil || originLocation == nil || stop == nil || stop.Location == nil {
		return nil
	}

	distanceMetres := int(math.Ceil(originLocation.Distance(stop.Location)))
	if distanceMetres < 0 {
		distanceMetres = 0
	}

	walkDurationSeconds := int(math.Ceil(float64(distanceMetres) / defaultJourneyPlanWalkSpeedMetresPerSecond))
	arrivalTime := startDateTime.Add(time.Duration(walkDurationSeconds) * time.Second)
	routeItem := ctdf.JourneyPlanRouteItem{
		Type:                 ctdf.JourneyPlanRouteItemTypeTransfer,
		TransferType:         ctdf.StopTransferTypeNearbyWalk,
		OriginStopRef:        originStop.PrimaryIdentifier,
		DestinationStopRef:   stop.PrimaryIdentifier,
		StartTime:            startDateTime,
		ArrivalTime:          arrivalTime,
		DistanceMetres:       distanceMetres,
		WalkDurationSeconds:  walkDurationSeconds,
		TotalDurationSeconds: walkDurationSeconds,
	}

	return &plannerLabel{
		stop:        stop,
		arrivalTime: arrivalTime,
		routeItems:  appendRouteItem(nil, routeItem),
	}
}

func (runtime *plannerRuntime) expandTransfers(pq *plannerPriorityQueue, current *plannerLabel, destinationStop *ctdf.Stop, results *ctdf.JourneyPlanResults) error {
	if runtime.searchExpired() {
		return nil
	}

	transfers, err := runtime.loadTransfers(current.stop)
	if err != nil {
		return err
	}

	forbidden := map[string]bool{}
	for _, transfer := range transfers {
		if transfer != nil && transfer.Type == ctdf.StopTransferTypeForbidden {
			forbidden[transfer.FromStopRef+"\x00"+transfer.ToStopRef] = true
		}
	}

	for _, transfer := range transfers {
		if runtime.searchExpired() {
			return nil
		}
		if transfer == nil || transfer.ToStopRef == "" || transfer.Type == ctdf.StopTransferTypeForbidden || !transferAppliesToLabel(transfer, current) {
			continue
		}
		if forbidden[transfer.FromStopRef+"\x00"+transfer.ToStopRef] {
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

		nextLabel := &plannerLabel{
			stop:                 toStop,
			arrivalTime:          arrivalTime,
			vehicleLegs:          current.vehicleLegs,
			consecutiveTransfers: current.consecutiveTransfers + 1,
			routeItems:           appendRouteItem(current.routeItems, routeItem),
			preferExpansion:      isHighCapacityStop(toStop),
		}
		if stopMatchesStop(toStop, destinationStop) {
			runtime.recordResult(results, nextLabel)
			if len(results.JourneyPlans) >= runtime.config.count {
				return nil
			}
			continue
		}

		if !runtime.pushLabel(pq, nextLabel) || nextLabel.vehicleLegs >= runtime.config.maxVehicleLegs {
			continue
		}

		if routeItemDepth(nextLabel.routeItems) < runtime.config.maxRouteItems && shouldScanDirectTransferDepartures(nextLabel.stop, destinationStop) {
			if err := runtime.recordDirectDeparturesToDestination(nextLabel, destinationStop, results); err != nil {
				return err
			}
			if len(results.JourneyPlans) >= runtime.config.count {
				return nil
			}
		}

		if runtime.shouldScanAccessInterchange(nextLabel, destinationStop) {
			if err := runtime.expandDepartures(pq, nextLabel, destinationStop, results); err != nil {
				return err
			}
			nextLabel.departuresExpanded = true
			if len(results.JourneyPlans) >= runtime.config.count {
				return nil
			}
		}
	}

	return nil
}

// Route/trip-restricted GTFS transfers must match the service just used. A
// destination restriction is enforced conservatively here: this planner cannot
// yet constrain the next boarding search by route/trip, so it must not offer a
// potentially invalid transfer.
func transferAppliesToLabel(transfer *ctdf.StopTransfer, current *plannerLabel) bool {
	if transfer.ToRouteRef != "" || transfer.ToTripRef != "" {
		return false
	}
	if transfer.FromRouteRef == "" && transfer.FromTripRef == "" {
		return true
	}
	if current == nil || current.routeItems == nil || current.routeItems.item.Journey == nil {
		return false
	}

	journey := current.routeItems.item.Journey
	if transfer.FromRouteRef != "" && transfer.FromRouteRef != journey.ServiceRef {
		return false
	}
	return transfer.FromTripRef == "" || transfer.FromTripRef == journey.PrimaryIdentifier
}

func (runtime *plannerRuntime) expandDepartures(pq *plannerPriorityQueue, current *plannerLabel, destinationStop *ctdf.Stop, results *ctdf.JourneyPlanResults) error {
	if runtime.searchExpired() {
		return nil
	}

	departureBoardCount := runtime.config.departureBoardCount
	// A coordinate origin already fans out across several nearby stops. Applying
	// the large single-origin board to every one of those walking labels multiplies
	// the search fan-out and often forces tomorrow's board to be generated too.
	if current.vehicleLegs == 0 && current.routeItems == nil {
		departureBoardCount = runtime.config.originDepartureBoardCount
	}

	departureBoard, err := runtime.loadDepartureBoard(current.stop, current.arrivalTime, departureBoardCount)
	if err != nil {
		return err
	}

	sort.Slice(departureBoard, func(i, j int) bool {
		return departureBoard[i].Time.Before(departureBoard[j].Time)
	})

	candidates := make([]departureCandidate, 0, len(departureBoard))

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

		reachesDestination := runtime.recordDirectDestinationFromDeparture(current, departure, boardingIndex, destinationStop, results)
		candidates = append(candidates, departureCandidate{
			departure:          departure,
			boardingIndex:      boardingIndex,
			reachesDestination: reachesDestination,
		})
		if reachesDestination && len(results.JourneyPlans) >= runtime.config.count {
			return nil
		}
	}
	// A cold graph lookup can finish just after the search deadline. The direct
	// scan above is bounded by the requested board count, so retain any route it
	// found before stopping further expansion.
	if runtime.searchExpired() {
		return nil
	}
	if err := runtime.prefetchDeparturePaths(candidates); err != nil {
		return err
	}

	for _, candidate := range candidates {
		if runtime.searchExpired() {
			return nil
		}
		if candidate.reachesDestination {
			continue
		}

		departure := candidate.departure
		boardingIndex := candidate.boardingIndex
		boardingStopRef := departure.Journey.Path[boardingIndex].OriginStopRef
		lastPathIndex := lastJourneyDestinationIndex(departure.Journey, boardingIndex)

		lastTime := departure.Time
		for pathIndex := boardingIndex; pathIndex < len(departure.Journey.Path); pathIndex++ {
			if runtime.searchExpired() {
				return nil
			}

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

			nextLabel := &plannerLabel{
				stop:            toStop,
				arrivalTime:     arrivalTime,
				vehicleLegs:     current.vehicleLegs + 1,
				routeItems:      appendRouteItem(current.routeItems, routeItem),
				preferExpansion: pathIndex == lastPathIndex && isHighCapacityStop(toStop),
			}
			if stopMatchesStop(toStop, destinationStop) {
				runtime.recordResult(results, nextLabel)
				if len(results.JourneyPlans) >= runtime.config.count {
					return nil
				}
				continue
			}

			runtime.pushLabel(pq, nextLabel)

		}
	}

	return nil
}

func (runtime *plannerRuntime) recordDirectDeparturesToDestination(current *plannerLabel, destinationStop *ctdf.Stop, results *ctdf.JourneyPlanResults) error {
	if runtime.searchExpired() || current == nil || current.stop == nil || destinationStop == nil {
		return nil
	}

	departureBoard, err := runtime.loadDepartureBoard(current.stop, current.arrivalTime, runtime.config.departureBoardCount)
	if err != nil {
		return err
	}

	for _, departure := range departureBoard {
		if departure == nil || departure.Journey == nil || len(departure.Journey.Path) == 0 || departure.Type == ctdf.DepartureBoardRecordTypeCancelled {
			continue
		}
		if departure.Time.Before(current.arrivalTime) || departure.Time.After(runtime.searchEndTime) {
			continue
		}

		boardingIndex := boardingPathIndex(departure.Journey, current.stop)
		if boardingIndex < 0 {
			continue
		}

		if runtime.recordDirectDestinationFromDeparture(current, departure, boardingIndex, destinationStop, results) && len(results.JourneyPlans) >= runtime.config.count {
			return nil
		}
	}

	return nil
}

func (runtime *plannerRuntime) recordDirectDestinationFromDeparture(current *plannerLabel, departure *ctdf.DepartureBoard, boardingIndex int, destinationStop *ctdf.Stop, results *ctdf.JourneyPlanResults) bool {
	if current == nil || departure == nil || departure.Journey == nil || destinationStop == nil {
		return false
	}
	if boardingIndex < 0 || boardingIndex >= len(departure.Journey.Path) {
		return false
	}

	boardingStopRef := departure.Journey.Path[boardingIndex].OriginStopRef
	lastTime := departure.Time
	recorded := false
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
		if !stopMatchesRef(destinationStop, pathItem.DestinationStopRef) {
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
		runtime.recordResult(results, &plannerLabel{
			stop:        destinationStop,
			arrivalTime: arrivalTime,
			vehicleLegs: current.vehicleLegs + 1,
			routeItems:  appendRouteItem(current.routeItems, routeItem),
		})
		recorded = true
	}

	return recorded
}

func (runtime *plannerRuntime) prefetchDeparturePaths(candidates []departureCandidate) error {
	missingRefs := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate.reachesDestination || candidate.departure == nil || candidate.departure.Journey == nil {
			continue
		}
		for pathIndex := candidate.boardingIndex; pathIndex < len(candidate.departure.Journey.Path); pathIndex++ {
			pathItem := candidate.departure.Journey.Path[pathIndex]
			if pathItem == nil || pathItem.DestinationStopRef == "" {
				continue
			}
			if _, exists := runtime.stopCache[pathItem.DestinationStopRef]; !exists {
				missingRefs[pathItem.DestinationStopRef] = struct{}{}
			}
		}
	}

	if len(missingRefs) > 0 {
		identifiers := make([]string, 0, len(missingRefs))
		for identifier := range missingRefs {
			identifiers = append(identifiers, identifier)
		}
		stopsCollection := database.GetCollection("stops")
		ctx, cancel := runtime.searchContext()
		cursor, err := stopsCollection.Find(ctx, bson.M{
			"$or": bson.A{
				bson.M{"primaryidentifier": bson.M{"$in": identifiers}},
				bson.M{"otheridentifiers": bson.M{"$in": identifiers}},
				bson.M{"platforms.primaryidentifier": bson.M{"$in": identifiers}},
			},
		}, options.Find().SetProjection(bson.D{{Key: "_id", Value: 0}}))
		if err != nil {
			cancel()
			return runtime.searchQueryError(err)
		}
		for cursor.Next(ctx) {
			var stop ctdf.Stop
			if err := cursor.Decode(&stop); err != nil {
				_ = cursor.Close(ctx)
				cancel()
				return err
			}
			runtime.cacheStop(&stop)
		}
		if err := cursor.Err(); err != nil {
			_ = cursor.Close(ctx)
			cancel()
			return runtime.searchQueryError(err)
		}
		_ = cursor.Close(ctx)
		cancel()
	}

	stops := make([]*ctdf.Stop, 0, len(missingRefs))
	seenStops := map[string]struct{}{}
	for _, candidate := range candidates {
		if candidate.reachesDestination || candidate.departure == nil || candidate.departure.Journey == nil {
			continue
		}
		for pathIndex := candidate.boardingIndex; pathIndex < len(candidate.departure.Journey.Path); pathIndex++ {
			pathItem := candidate.departure.Journey.Path[pathIndex]
			if pathItem == nil {
				continue
			}
			stop := runtime.stopCache[pathItem.DestinationStopRef]
			if stop == nil {
				continue
			}
			if _, exists := seenStops[stop.PrimaryIdentifier]; exists {
				continue
			}
			seenStops[stop.PrimaryIdentifier] = struct{}{}
			stops = append(stops, stop)
		}
	}
	return runtime.prefetchTransfers(stops)
}

func (runtime *plannerRuntime) prefetchTransfers(stops []*ctdf.Stop) error {
	pending := map[string]*ctdf.Stop{}
	identifiers := make([]string, 0, len(stops))
	for _, stop := range stops {
		if stop == nil {
			continue
		}
		if _, exists := runtime.transferCache[stop.PrimaryIdentifier]; exists {
			continue
		}
		pending[stop.PrimaryIdentifier] = stop
		identifiers = append(identifiers, stop.GetAllStopIDs()...)
	}
	if len(pending) == 0 {
		return nil
	}

	stopTransfersCollection := database.GetCollection("stop_transfers")
	ctx, cancel := runtime.searchContext()
	defer cancel()
	cursor, err := stopTransfersCollection.Find(ctx, bson.M{
		"fromstopref": bson.M{"$in": identifiers},
	}, options.Find().SetProjection(bson.D{{Key: "_id", Value: 0}}))
	if err != nil {
		return runtime.searchQueryError(err)
	}
	defer cursor.Close(ctx)

	prefetched := make(map[string][]*ctdf.StopTransfer, len(pending))
	for identifier := range pending {
		prefetched[identifier] = nil
	}
	for cursor.Next(ctx) {
		var transfer ctdf.StopTransfer
		if err := cursor.Decode(&transfer); err != nil {
			return err
		}
		fromStop := runtime.stopCache[transfer.FromStopRef]
		if fromStop == nil {
			continue
		}
		if _, exists := pending[fromStop.PrimaryIdentifier]; !exists {
			continue
		}
		prefetched[fromStop.PrimaryIdentifier] = append(prefetched[fromStop.PrimaryIdentifier], &transfer)
	}
	if err := cursor.Err(); err != nil {
		return runtime.searchQueryError(err)
	}
	for identifier, transfers := range prefetched {
		runtime.transferCache[identifier] = transfers
	}
	return nil
}

func (runtime *plannerRuntime) searchContext() (context.Context, context.CancelFunc) {
	if runtime.searchDeadline.IsZero() {
		return context.WithCancel(context.Background())
	}
	return context.WithDeadline(context.Background(), runtime.searchDeadline)
}

func (runtime *plannerRuntime) searchQueryError(err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		runtime.timedOut = true
		return nil
	}
	return err
}

// loadDepartureBoard returns a bounded departure board for a stop while caching
// the full generated board for that calendar day. Later visits advance through
// the cached board instead of regenerating the same stop for every arrival label.
func (runtime *plannerRuntime) loadDepartureBoard(stop *ctdf.Stop, startDateTime time.Time, departureBoardCount int) ([]*ctdf.DepartureBoard, error) {
	if stop == nil {
		return nil, nil
	}

	if cached, exists := runtime.departureBoardCache[stop.PrimaryIdentifier]; exists {
		if !startDateTime.Before(cached.fetchTime) &&
			sameCalendarDay(cached.fetchTime, startDateTime) &&
			cached.count >= departureBoardCount {
			filtered := make([]*ctdf.DepartureBoard, 0, len(cached.board))
			for _, departure := range cached.board {
				if departure == nil || departure.Time.Before(startDateTime) {
					continue
				}
				filtered = append(filtered, departure)
			}
			return limitDepartureBoard(filtered, departureBoardCount), nil
		}
	}

	board, err := runtime.lookupDepartureBoard(query.DepartureBoard{
		Stop:            stop,
		DestinationStop: runtime.destinationStop,
		Count:           departureBoardCount,
		StartDateTime:   startDateTime,
	})
	if err != nil {
		return nil, err
	}
	// The departure-board source already generated the whole day's board. Keep
	// that full result in this search's cache so a later arrival at the same stop
	// can advance past the first page without regenerating it.
	board = limitDepartureBoard(board, 0)

	runtime.departureBoardCache[stop.PrimaryIdentifier] = cachedDepartureBoard{
		fetchTime: startDateTime,
		count:     departureBoardCount,
		board:     board,
	}

	return limitDepartureBoard(board, departureBoardCount), nil
}

func (runtime *plannerRuntime) lookupDepartureBoard(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
	if runtime.departureBoardLookup != nil {
		return runtime.departureBoardLookup(q)
	}
	return dataaggregator.Lookup[[]*ctdf.DepartureBoard](q)
}

func (runtime *plannerRuntime) searchExpired() bool {
	if runtime.timedOut {
		return true
	}
	if runtime.searchDeadline.IsZero() || time.Now().Before(runtime.searchDeadline) {
		return false
	}

	runtime.timedOut = true
	return true
}

func limitDepartureBoard(board []*ctdf.DepartureBoard, count int) []*ctdf.DepartureBoard {
	if len(board) == 0 {
		return board
	}

	limited := make([]*ctdf.DepartureBoard, 0, len(board))
	for _, departure := range board {
		if departure != nil {
			limited = append(limited, departure)
		}
	}

	sort.Slice(limited, func(i, j int) bool {
		return limited[i].Time.Before(limited[j].Time)
	})

	if count > 0 && len(limited) > count {
		limited = limited[:count]
	}

	return limited
}

func sameCalendarDay(a time.Time, b time.Time) bool {
	yearA, monthA, dayA := a.Date()
	yearB, monthB, dayB := b.Date()
	return yearA == yearB && monthA == monthB && dayA == dayB
}

func (runtime *plannerRuntime) loadOriginLocationStops(location *ctdf.Location) ([]*ctdf.Stop, error) {
	if location == nil || len(location.Coordinates) != 2 {
		return nil, nil
	}

	stopsCollection := database.GetCollection("stops")
	opts := options.Find().
		SetProjection(bson.D{
			bson.E{Key: "_id", Value: 0},
		}).
		SetLimit(int64(runtime.config.originLocationStopCount))

	cursor, err := stopsCollection.Find(context.Background(), bson.M{
		"location": bson.M{
			"$near": bson.M{
				"$geometry": bson.M{
					"type":        "Point",
					"coordinates": bson.A{location.Coordinates[0], location.Coordinates[1]},
				},
				"$maxDistance": runtime.config.maxTransferDistance,
			},
		},
	}, opts)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.Background())

	stops := make([]*ctdf.Stop, 0, runtime.config.originLocationStopCount)
	for cursor.Next(context.Background()) {
		var stop ctdf.Stop
		if err := cursor.Decode(&stop); err != nil {
			return nil, err
		}
		runtime.cacheStop(&stop)
		stops = append(stops, &stop)
	}
	if err := cursor.Err(); err != nil {
		return nil, err
	}

	return stops, nil
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

func (runtime *plannerRuntime) pushLabel(pq *plannerPriorityQueue, label *plannerLabel) bool {
	if label == nil || label.stop == nil || label.arrivalTime.After(runtime.searchEndTime) {
		return false
	}
	label.priorityTime = runtime.queuePriority(label)
	label.priorityClass = runtime.queuePriorityClass(label)

	key := plannerStateKey{
		stopRef:              label.stop.PrimaryIdentifier,
		vehicleLegs:          label.vehicleLegs,
		consecutiveTransfers: label.consecutiveTransfers,
	}

	arrivals := runtime.bestArrivals[key]
	for _, arrival := range arrivals {
		if arrival.Equal(label.arrivalTime) {
			return false
		}
	}

	// arrivals is kept sorted ascending as an invariant (we always re-sort
	// before storing below), so the worst kept arrival is the last element.
	if len(arrivals) >= runtime.config.maxLabelsPerState {
		lastIndex := len(arrivals) - 1
		if !label.arrivalTime.Before(arrivals[lastIndex]) {
			return false
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
	return true
}

func (runtime *plannerRuntime) queuePriorityClass(label *plannerLabel) uint8 {
	if label == nil || label.stop == nil {
		return 2
	}
	// Always expand the requested origin, high-capacity transfer targets, and the
	// end of a vehicle journey before intermediate calls. Without this
	// distinction a stopping train can spend the whole search budget rebuilding
	// boards at each early station while its useful terminal interchange remains
	// in the queue.
	if label.preferExpansion || stopMatchesStop(label.stop, runtime.destinationStop) {
		return 0
	}
	if runtime.destinationStop != nil && isHighCapacityStop(label.stop) && isHighCapacityStop(runtime.destinationStop) {
		return 1
	}
	return 2
}

func (runtime *plannerRuntime) queuePriority(label *plannerLabel) time.Time {
	if label == nil {
		return time.Time{}
	}
	if runtime.destinationStop == nil || runtime.destinationStop.Location == nil || label.stop == nil || label.stop.Location == nil {
		return label.arrivalTime
	}
	// Straight-line travel at an intentionally generous maximum speed is an
	// admissible lower bound. The separate priority class keeps high-capacity
	// progress ahead of local-network labels for high-capacity destinations.
	distanceMetres := label.stop.Location.Distance(runtime.destinationStop.Location)
	if distanceMetres <= 0 {
		return label.arrivalTime
	}
	seconds := math.Ceil(distanceMetres / defaultJourneyPlanMaximumSpeedMetresPerSecond)
	return label.arrivalTime.Add(time.Duration(seconds) * time.Second)
}

func (runtime *plannerRuntime) isCurrentLabel(label *plannerLabel) bool {
	if label == nil || label.stop == nil {
		return false
	}

	key := plannerStateKey{
		stopRef:              label.stop.PrimaryIdentifier,
		vehicleLegs:          label.vehicleLegs,
		consecutiveTransfers: label.consecutiveTransfers,
	}
	for _, arrival := range runtime.bestArrivals[key] {
		if arrival.Equal(label.arrivalTime) {
			return true
		}
	}

	return false
}

func (runtime *plannerRuntime) recordResult(results *ctdf.JourneyPlanResults, label *plannerLabel) {
	if results == nil || label == nil || label.routeItems == nil {
		return
	}

	plan := buildJourneyPlan(label)
	key := journeyPlanKey(plan)
	if runtime.resultKeys[key] {
		return
	}

	results.JourneyPlans = append(results.JourneyPlans, plan)
	runtime.resultKeys[key] = true
}

func buildJourneyPlan(label *plannerLabel) ctdf.JourneyPlan {
	routeItems := routeItemsSlice(label.routeItems)

	startTime := label.arrivalTime
	if len(routeItems) > 0 {
		startTime = routeItems[0].StartTime
	}

	return ctdf.JourneyPlan{
		RouteItems:  routeItems,
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

func appendRouteItem(parent *routeItemNode, routeItem ctdf.JourneyPlanRouteItem) *routeItemNode {
	depth := 1
	if parent != nil {
		depth = parent.depth + 1
	}

	return &routeItemNode{
		item:   routeItem,
		parent: parent,
		depth:  depth,
	}
}

func routeItemDepth(node *routeItemNode) int {
	if node == nil {
		return 0
	}

	return node.depth
}

func routeItemsSlice(node *routeItemNode) []ctdf.JourneyPlanRouteItem {
	routeItems := make([]ctdf.JourneyPlanRouteItem, routeItemDepth(node))
	for node != nil {
		routeItems[node.depth-1] = node.item
		node = node.parent
	}

	return routeItems
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

func lastJourneyDestinationIndex(journey *ctdf.Journey, boardingIndex int) int {
	if journey == nil {
		return -1
	}

	for index := len(journey.Path) - 1; index >= boardingIndex; index-- {
		pathItem := journey.Path[index]
		if pathItem != nil && pathItem.DestinationStopRef != "" {
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

func (runtime *plannerRuntime) shouldScanAccessInterchange(label *plannerLabel, destinationStop *ctdf.Stop) bool {
	if label == nil || label.stop == nil || destinationStop == nil {
		return false
	}
	if label.stop.Location == nil || destinationStop.Location == nil {
		return false
	}
	if label.vehicleLegs > defaultJourneyPlanAccessVehicleLegs || label.vehicleLegs+2 > runtime.config.maxVehicleLegs {
		return false
	}
	if routeItemDepth(label.routeItems)+3 > runtime.config.maxRouteItems || !isHighCapacityStop(label.stop) {
		return false
	}

	return !stopsWithinDistance(label.stop, destinationStop, defaultJourneyPlanDestinationApproachMetres)
}

func shouldScanDirectTransferDepartures(stop *ctdf.Stop, destinationStop *ctdf.Stop) bool {
	return isHighCapacityStop(stop) && stopsWithinDistance(stop, destinationStop, defaultJourneyPlanDestinationApproachMetres)
}

func isHighCapacityStop(stop *ctdf.Stop) bool {
	if stop == nil {
		return false
	}

	for _, transportType := range stop.TransportTypes {
		switch transportType {
		case ctdf.TransportTypeRail, ctdf.TransportTypeMetro, ctdf.TransportTypeTram:
			return true
		}
	}

	return false
}

func stopsWithinDistance(stop *ctdf.Stop, destinationStop *ctdf.Stop, maxDistanceMetres int) bool {
	if stop == nil || stop.Location == nil || destinationStop == nil || destinationStop.Location == nil || maxDistanceMetres < 0 {
		return false
	}

	return stop.Location.Distance(destinationStop.Location) <= float64(maxDistanceMetres)
}
