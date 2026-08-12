package departuregraph

import (
	"container/heap"
	"context"
	"errors"
	"math"
	"sort"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/travigo/travigo/pkg/ctdf"
)

const (
	defaultPlanCount             = 5
	defaultPlanMaxChanges        = 3
	defaultPlanDuration          = 12 * time.Hour
	defaultPlanTransferDistance  = 1000
	defaultPlanOriginStops       = 12
	defaultPlanExpandedLabels    = 200000
	defaultPlanSearchDuration    = 10 * time.Second
	planWalkSpeedMetresPerSecond = 1.3
	planMaximumNetworkSpeed      = 100.0
)

var ErrGraphNotReady = errors.New("journey graph is not ready for the requested service day")

type PlanLocation struct {
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

type PlanRequest struct {
	OriginRefs                []string      `json:"originRefs,omitempty"`
	OriginLocation            *PlanLocation `json:"originLocation,omitempty"`
	DestinationRefs           []string      `json:"destinationRefs"`
	StartDateTime             time.Time     `json:"startDateTime"`
	Count                     int           `json:"count,omitempty"`
	MaxChanges                int           `json:"maxChanges,omitempty"`
	MaxJourneyDurationSeconds int           `json:"maxJourneyDurationSeconds,omitempty"`
	MaxTransferDistanceMetres int           `json:"maxTransferDistanceMetres,omitempty"`
	OriginLocationStopCount   int           `json:"originLocationStopCount,omitempty"`
	MaxExpandedLabels         int           `json:"maxExpandedLabels,omitempty"`
	MaxSearchDurationMillis   int           `json:"maxSearchDurationMillis,omitempty"`
}

type PlanResponse struct {
	Plans                 []Plan `json:"plans"`
	SearchTruncated       bool   `json:"searchTruncated,omitempty"`
	SearchTruncatedReason string `json:"searchTruncatedReason,omitempty"`
}

type Plan struct {
	Legs        []PlanLeg `json:"legs"`
	StartTime   time.Time `json:"startTime"`
	ArrivalTime time.Time `json:"arrivalTime"`
}

type PlanLeg struct {
	Type                     ctdf.JourneyPlanRouteItemType `json:"type"`
	JourneyRef               string                        `json:"journeyRef,omitempty"`
	TransferType             ctdf.StopTransferType         `json:"transferType,omitempty"`
	OriginStopRef            string                        `json:"originStopRef"`
	DestinationStopRef       string                        `json:"destinationStopRef"`
	StartTime                time.Time                     `json:"startTime"`
	ArrivalTime              time.Time                     `json:"arrivalTime"`
	DistanceMetres           int                           `json:"distanceMetres,omitempty"`
	WalkDurationSeconds      int                           `json:"walkDurationSeconds,omitempty"`
	MinChangeDurationSeconds int                           `json:"minChangeDurationSeconds,omitempty"`
	TotalDurationSeconds     int                           `json:"totalDurationSeconds,omitempty"`
}

type planConfig struct {
	count               int
	maxVehicleLegs      int
	maxDuration         time.Duration
	maxTransferDistance int
	originStops         int
	maxExpandedLabels   int
	maxSearchDuration   time.Duration
}

type planRouteNode struct {
	leg    PlanLeg
	parent *planRouteNode
	depth  int
}

type planLabel struct {
	stop        uint32
	arrival     time.Time
	vehicleLegs int
	route       *planRouteNode
	index       int
}

type planQueue struct {
	labels           []*planLabel
	data             *graphData
	destinations     []PlanLocation
	heuristicSeconds []uint32
	corridor         []uint8
	maxVehicleLegs   int
}

func (q planQueue) Len() int { return len(q.labels) }
func (q planQueue) Less(i, j int) bool {
	iPriority := q.priority(q.labels[i])
	jPriority := q.priority(q.labels[j])
	if iPriority.Equal(jPriority) {
		return q.labels[i].arrival.Before(q.labels[j].arrival)
	}
	return iPriority.Before(jPriority)
}
func (q planQueue) Swap(i, j int) {
	q.labels[i], q.labels[j] = q.labels[j], q.labels[i]
	q.labels[i].index = i
	q.labels[j].index = j
}
func (q *planQueue) Push(value any) {
	label := value.(*planLabel)
	label.index = len(q.labels)
	q.labels = append(q.labels, label)
}
func (q *planQueue) Pop() any {
	old := q.labels
	label := old[len(old)-1]
	old[len(old)-1] = nil
	q.labels = old[:len(old)-1]
	return label
}

func (q *planQueue) priority(label *planLabel) time.Time {
	if q == nil || q.data == nil || int(label.stop) >= len(q.heuristicSeconds) || len(q.destinations) == 0 {
		return label.arrival
	}
	encodedSeconds := q.heuristicSeconds[label.stop]
	if encodedSeconds == 0 {
		stop := q.data.Stops[label.stop]
		seconds := uint32(0)
		if stop.HasLocation {
			minimumDistance := math.MaxFloat64
			for _, destination := range q.destinations {
				distance := distanceMetres(float64(stop.Longitude), float64(stop.Latitude), destination.Longitude, destination.Latitude)
				if distance < minimumDistance {
					minimumDistance = distance
				}
			}
			seconds = uint32(minimumDistance / planMaximumNetworkSpeed)
		}
		encodedSeconds = seconds + 1
		q.heuristicSeconds[label.stop] = encodedSeconds
	}
	return label.arrival.Add(time.Duration(encodedSeconds-1) * time.Second)
}

type planState struct {
	stop        uint32
	vehicleLegs int
	transferred bool
}

func (g *Graph) Plan(ctx context.Context, request PlanRequest) (PlanResponse, error) {
	started := time.Now()
	if g == nil {
		return PlanResponse{}, ErrGraphNotReady
	}
	data := g.current.Load()
	data.mu.RLock()
	defer data.mu.RUnlock()
	if !data.TopologyReady || len(data.Stops) == 0 || len(data.TransferOffsets) != len(data.Stops)+1 {
		return PlanResponse{}, ErrGraphNotReady
	}
	config := normalizePlanConfig(request)
	if request.StartDateTime.IsZero() {
		request.StartDateTime = time.Now()
	}
	if !data.CompleteDays[makeDayKey(request.StartDateTime)] {
		return PlanResponse{}, ErrGraphNotReady
	}
	deadline := time.Now().Add(config.maxSearchDuration)
	destinationNodes := data.resolveStopSet(request.DestinationRefs)
	if len(destinationNodes) == 0 {
		return PlanResponse{Plans: []Plan{}}, nil
	}

	queue := newPlanQueue(data, destinationNodes)
	queue.corridor = data.planCorridor(destinationNodes, config.maxVehicleLegs)
	queue.maxVehicleLegs = config.maxVehicleLegs
	heap.Init(queue)
	best := map[planState]time.Time{}
	originRef := "coordinate-origin"
	if len(request.OriginRefs) > 0 {
		originRef = request.OriginRefs[0]
		for node := range data.resolveStopSet(request.OriginRefs) {
			pushPlanLabel(queue, best, &planLabel{stop: node, arrival: request.StartDateTime})
		}
	} else if request.OriginLocation != nil {
		nearbyStops := data.nearbyStops(*request.OriginLocation, config.maxTransferDistance, config.originStops)
		seenNearby := make(map[uint32]bool, len(nearbyStops))
		for _, nearby := range nearbyStops {
			seenNearby[nearby.stop] = true
		}
		for destination := range destinationNodes {
			stop := data.Stops[destination]
			if seenNearby[destination] || !stop.HasLocation {
				continue
			}
			distance := distanceMetres(request.OriginLocation.Longitude, request.OriginLocation.Latitude, float64(stop.Longitude), float64(stop.Latitude))
			if distance <= float64(config.maxTransferDistance) {
				nearbyStops = append(nearbyStops, nearbyStop{stop: destination, distance: distance})
			}
		}
		for _, nearby := range nearbyStops {
			duration := int(math.Ceil(nearby.distance / planWalkSpeedMetresPerSecond))
			arrival := request.StartDateTime.Add(time.Duration(duration) * time.Second)
			pushPlanLabel(queue, best, &planLabel{
				stop:    nearby.stop,
				arrival: arrival,
				route: appendPlanLeg(nil, PlanLeg{
					Type:                 ctdf.JourneyPlanRouteItemTypeTransfer,
					TransferType:         ctdf.StopTransferTypeNearbyWalk,
					OriginStopRef:        originRef,
					DestinationStopRef:   data.stringValue(data.Stops[nearby.stop].PrimaryRef),
					StartTime:            request.StartDateTime,
					ArrivalTime:          arrival,
					DistanceMetres:       int(math.Ceil(nearby.distance)),
					WalkDurationSeconds:  duration,
					TotalDurationSeconds: duration,
				}),
			})
		}
	}

	response := PlanResponse{Plans: []Plan{}}
	resultKeys := map[string]bool{}
	expanded := 0
	searchEnd := request.StartDateTime.Add(config.maxDuration)
	for queue.Len() > 0 && len(response.Plans) < config.count && expanded < config.maxExpandedLabels && time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return PlanResponse{}, ctx.Err()
		default:
		}
		current := heap.Pop(queue).(*planLabel)
		if !currentPlanLabel(best, current) {
			continue
		}
		expanded++
		if destinationNodes[current.stop] && current.route != nil {
			plan := materializePlan(current)
			key := planKey(plan)
			if !resultKeys[key] {
				resultKeys[key] = true
				response.Plans = append(response.Plans, plan)
			}
			continue
		}
		if current.arrival.After(searchEnd) {
			continue
		}
		data.expandPlanTransfers(queue, best, config, current, searchEnd)
		if current.vehicleLegs < config.maxVehicleLegs {
			data.expandPlanJourneys(queue, best, config, current, searchEnd)
		}
	}
	if len(response.Plans) < config.count && queue.Len() > 0 {
		response.SearchTruncated = true
		if expanded >= config.maxExpandedLabels {
			response.SearchTruncatedReason = "expanded_label_budget"
		} else if !time.Now().Before(deadline) {
			response.SearchTruncatedReason = "time_budget"
		}
	}
	sort.Slice(response.Plans, func(i, j int) bool {
		if response.Plans[i].StartTime.Equal(response.Plans[j].StartTime) {
			return response.Plans[i].ArrivalTime.Before(response.Plans[j].ArrivalTime)
		}
		return response.Plans[i].StartTime.Before(response.Plans[j].StartTime)
	})
	log.Info().
		Int("expanded_labels", expanded).
		Int("plans", len(response.Plans)).
		Bool("truncated", response.SearchTruncated).
		Str("truncated_reason", response.SearchTruncatedReason).
		Dur("duration", time.Since(started)).
		Msg("Journey graph plan complete")
	return response, nil
}

func newPlanQueue(data *graphData, destinationNodes map[uint32]bool) *planQueue {
	queue := &planQueue{data: data, heuristicSeconds: make([]uint32, len(data.Stops))}
	for destination := range destinationNodes {
		if int(destination) >= len(data.Stops) {
			continue
		}
		stop := data.Stops[destination]
		if stop.HasLocation {
			queue.destinations = append(queue.destinations, PlanLocation{Longitude: float64(stop.Longitude), Latitude: float64(stop.Latitude)})
		}
	}
	return queue
}

func normalizePlanConfig(request PlanRequest) planConfig {
	config := planConfig{count: request.Count, maxVehicleLegs: request.MaxChanges + 1, maxDuration: time.Duration(request.MaxJourneyDurationSeconds) * time.Second, maxTransferDistance: request.MaxTransferDistanceMetres, originStops: request.OriginLocationStopCount, maxExpandedLabels: request.MaxExpandedLabels, maxSearchDuration: time.Duration(request.MaxSearchDurationMillis) * time.Millisecond}
	if config.count <= 0 {
		config.count = defaultPlanCount
	}
	if request.MaxChanges < 0 {
		config.maxVehicleLegs = defaultPlanMaxChanges + 1
	}
	if config.maxVehicleLegs <= 0 {
		config.maxVehicleLegs = defaultPlanMaxChanges + 1
	}
	if config.maxDuration <= 0 {
		config.maxDuration = defaultPlanDuration
	}
	if config.maxTransferDistance <= 0 {
		config.maxTransferDistance = defaultPlanTransferDistance
	}
	if config.originStops <= 0 {
		config.originStops = defaultPlanOriginStops
	}
	if config.maxExpandedLabels <= 0 {
		config.maxExpandedLabels = defaultPlanExpandedLabels
	}
	if config.maxSearchDuration <= 0 {
		config.maxSearchDuration = defaultPlanSearchDuration
	}
	return config
}

func (d *graphData) resolveStopSet(refs []string) map[uint32]bool {
	result := map[uint32]bool{}
	for _, ref := range refs {
		if stop, exists := d.stopIndex(ref); exists {
			result[stop] = true
		}
	}
	return result
}

type nearbyStop struct {
	stop     uint32
	distance float64
}

func (d *graphData) nearbyStops(location PlanLocation, maxDistance int, limit int) []nearbyStop {
	base := cellForCoordinates(location.Longitude, location.Latitude)
	latitudeRadius := int(math.Ceil(float64(maxDistance)/(111320.0*spatialCellDegrees))) + 1
	longitudeScale := math.Abs(math.Cos(location.Latitude * math.Pi / 180))
	if longitudeScale < 0.01 {
		longitudeScale = 0.01
	}
	longitudeRadius := int(math.Ceil(float64(maxDistance)/(111320.0*spatialCellDegrees*longitudeScale))) + 1
	candidates := make([]nearbyStop, 0, limit*2)
	for lon := -longitudeRadius; lon <= longitudeRadius; lon++ {
		for lat := -latitudeRadius; lat <= latitudeRadius; lat++ {
			cell := spatialCell{Longitude: base.Longitude + int16(lon), Latitude: base.Latitude + int16(lat)}
			for _, stop := range d.StopGrid[cell] {
				if int(stop) >= len(d.Stops) || !d.Stops[stop].HasLocation {
					continue
				}
				distance := distanceMetres(location.Longitude, location.Latitude, float64(d.Stops[stop].Longitude), float64(d.Stops[stop].Latitude))
				if distance <= float64(maxDistance) {
					candidates = append(candidates, nearbyStop{stop: stop, distance: distance})
				}
			}
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].distance < candidates[j].distance })
	if limit > 0 && len(candidates) > limit {
		candidates = candidates[:limit]
	}
	return candidates
}

func distanceMetres(lon1, lat1, lon2, lat2 float64) float64 {
	const radius = 6378100.0
	lat1 *= math.Pi / 180
	lat2 *= math.Pi / 180
	dlat := lat2 - lat1
	dlon := (lon2 - lon1) * math.Pi / 180
	a := math.Sin(dlat/2)*math.Sin(dlat/2) + math.Cos(lat1)*math.Cos(lat2)*math.Sin(dlon/2)*math.Sin(dlon/2)
	return 2 * radius * math.Asin(math.Sqrt(a))
}

func (d *graphData) expandPlanTransfers(queue *planQueue, best map[planState]time.Time, config planConfig, current *planLabel, searchEnd time.Time) {
	// Stop-transfer records model a single access, egress or interchange walk;
	// they are not a pedestrian street network. Chaining them creates fake
	// all-walking routes through sequences of nearby public-transport stops.
	if current.route != nil && current.route.leg.Type == ctdf.JourneyPlanRouteItemTypeTransfer {
		return
	}
	start, end := d.TransferOffsets[current.stop], d.TransferOffsets[current.stop+1]
	for index := start; index < end; index++ {
		transfer := d.Transfers[index]
		if transfer.DistanceMetres > uint32(config.maxTransferDistance) {
			continue
		}
		arrival := current.arrival.Add(time.Duration(transfer.TotalDurationSeconds) * time.Second)
		if arrival.After(searchEnd) {
			continue
		}
		pushPlanLabel(queue, best, &planLabel{stop: transfer.ToStop, arrival: arrival, vehicleLegs: current.vehicleLegs, route: appendPlanLeg(current.route, PlanLeg{
			Type:               ctdf.JourneyPlanRouteItemTypeTransfer,
			TransferType:       unpackTransferType(transfer.Type),
			OriginStopRef:      d.stringValue(d.Stops[current.stop].PrimaryRef),
			DestinationStopRef: d.stringValue(d.Stops[transfer.ToStop].PrimaryRef),
			StartTime:          current.arrival, ArrivalTime: arrival,
			DistanceMetres: int(transfer.DistanceMetres), WalkDurationSeconds: int(transfer.WalkDurationSeconds), MinChangeDurationSeconds: int(transfer.MinChangeDurationSeconds), TotalDurationSeconds: int(transfer.TotalDurationSeconds),
		})})
	}
}

func (d *graphData) expandPlanJourneys(queue *planQueue, best map[planState]time.Time, config planConfig, current *planLabel, searchEnd time.Time) {
	journeys := map[journeyDayKey]struct{}{}
	for dayOffset := 0; dayOffset <= 1; dayOffset++ {
		date := current.arrival.AddDate(0, 0, dayOffset)
		day := makeDayKey(date)
		if !d.CompleteDays[day] {
			continue
		}
		aliasStart, aliasEnd := d.StopAliasOffsets[current.stop], d.StopAliasOffsets[current.stop+1]
		for _, alias := range d.StopAliases[aliasStart:aliasEnd] {
			for _, journey := range d.Departures[bucketKey{Day: day, StopRef: alias}] {
				journeys[journeyDayKey{Day: day, Journey: journey}] = struct{}{}
			}
		}
	}
	for active := range journeys {
		if int(active.Journey) >= len(d.Journeys) {
			continue
		}
		record := d.Journeys[active.Journey]
		journeyRef := d.stringValue(record.PrimaryID)
		// A ride expansion already reaches every downstream alighting stop.
		// Re-boarding the same vehicle at its next stop only creates segmented
		// duplicates of that ride and incorrectly consumes another change.
		if current.route != nil && current.route.leg.Type == ctdf.JourneyPlanRouteItemTypeJourney && current.route.leg.JourneyRef == journeyRef {
			continue
		}
		serviceDate := dayKeyDate(active.Day, current.arrival.Location())
		boardingIndex := -1
		var departure time.Time
		for index := uint32(0); index < record.PathCount; index++ {
			path := d.Paths[record.PathStart+index]
			stop, exists := d.stopIndexForStringID(path.OriginStopRef)
			if !exists || stop != current.stop || path.OriginActivity == activitySetdown {
				continue
			}
			candidate := serviceDate.Add(time.Duration(path.OriginDeparture) * time.Second)
			if candidate.Before(current.arrival) {
				continue
			}
			boardingIndex = int(index)
			departure = candidate
			break
		}
		if boardingIndex < 0 || departure.After(searchEnd) {
			continue
		}
		for index := boardingIndex; index < int(record.PathCount); index++ {
			path := d.Paths[record.PathStart+uint32(index)]
			if path.DestinationActivity == activityPickup {
				continue
			}
			stop, exists := d.stopIndexForStringID(path.DestinationStopRef)
			if !exists {
				continue
			}
			arrival := serviceDate.Add(time.Duration(path.DestinationArrival) * time.Second)
			if arrival.Before(departure) {
				arrival = arrival.Add(24 * time.Hour)
			}
			if arrival.After(searchEnd) {
				break
			}
			pushPlanLabel(queue, best, &planLabel{stop: stop, arrival: arrival, vehicleLegs: current.vehicleLegs + 1, route: appendPlanLeg(current.route, PlanLeg{
				Type:               ctdf.JourneyPlanRouteItemTypeJourney,
				JourneyRef:         journeyRef,
				OriginStopRef:      d.stringValue(d.Paths[record.PathStart+uint32(boardingIndex)].OriginStopRef),
				DestinationStopRef: d.stringValue(path.DestinationStopRef),
				StartTime:          departure, ArrivalTime: arrival,
			})})
		}
	}
}

func pushPlanLabel(queue *planQueue, best map[planState]time.Time, label *planLabel) bool {
	if queue != nil && len(queue.corridor) > 0 {
		remainingVehicleLegs := queue.maxVehicleLegs - label.vehicleLegs
		if remainingVehicleLegs < 0 || int(label.stop) >= len(queue.corridor) || queue.corridor[label.stop] > uint8(remainingVehicleLegs) {
			return false
		}
	}
	state := stateForPlanLabel(label)
	if arrival, exists := best[state]; exists && !label.arrival.Before(arrival) {
		return false
	}
	best[state] = label.arrival
	heap.Push(queue, label)
	return true
}

func currentPlanLabel(best map[planState]time.Time, label *planLabel) bool {
	return best[stateForPlanLabel(label)].Equal(label.arrival)
}

func stateForPlanLabel(label *planLabel) planState {
	transferred := label.route != nil && label.route.leg.Type == ctdf.JourneyPlanRouteItemTypeTransfer
	return planState{stop: label.stop, vehicleLegs: label.vehicleLegs, transferred: transferred}
}

func appendPlanLeg(parent *planRouteNode, leg PlanLeg) *planRouteNode {
	depth := 1
	if parent != nil {
		depth = parent.depth + 1
	}
	return &planRouteNode{leg: leg, parent: parent, depth: depth}
}

func materializePlan(label *planLabel) Plan {
	legs := make([]PlanLeg, label.route.depth)
	for node := label.route; node != nil; node = node.parent {
		legs[node.depth-1] = node.leg
	}
	return Plan{Legs: legs, StartTime: legs[0].StartTime, ArrivalTime: label.arrival}
}

func planKey(plan Plan) string {
	key := ""
	for _, leg := range plan.Legs {
		key += string(leg.Type) + "\x00" + leg.JourneyRef + "\x00" + leg.OriginStopRef + "\x00" + leg.DestinationStopRef + "\x00" + leg.StartTime.String() + "\x00"
	}
	return key
}
