package departuregraph

import (
	"container/heap"
	"context"
	"errors"
	"fmt"
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
	defaultPlanLabelsPerState    = 1
	planVehicleLegPenalty        = 12 * time.Minute
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
	DestinationRefs           []string      `json:"destinationRefs,omitempty"`
	DestinationLocation       *PlanLocation `json:"destinationLocation,omitempty"`
	ExcludedJourneyRefs       []string      `json:"excludedJourneyRefs,omitempty"`
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
	ExpandedLabels        int    `json:"expandedLabels,omitempty"`
	SearchDurationMillis  int64  `json:"searchDurationMillis,omitempty"`
	FirstPlanMillis       int64  `json:"firstPlanMillis,omitempty"`
}

type Plan struct {
	Legs        []PlanLeg `json:"legs"`
	StartTime   time.Time `json:"startTime"`
	ArrivalTime time.Time `json:"arrivalTime"`
}

type PlanLeg struct {
	Type                        ctdf.JourneyPlanRouteItemType `json:"type"`
	JourneyRef                  string                        `json:"journeyRef,omitempty"`
	JourneyOriginStopIndex      int                           `json:"journeyOriginStopIndex,omitempty"`
	JourneyDestinationStopIndex int                           `json:"journeyDestinationStopIndex,omitempty"`
	TransferType                ctdf.StopTransferType         `json:"transferType,omitempty"`
	OriginStopRef               string                        `json:"originStopRef"`
	DestinationStopRef          string                        `json:"destinationStopRef"`
	StartTime                   time.Time                     `json:"startTime"`
	ArrivalTime                 time.Time                     `json:"arrivalTime"`
	DistanceMetres              int                           `json:"distanceMetres,omitempty"`
	WalkDurationSeconds         int                           `json:"walkDurationSeconds,omitempty"`
	MinChangeDurationSeconds    int                           `json:"minChangeDurationSeconds,omitempty"`
	TotalDurationSeconds        int                           `json:"totalDurationSeconds,omitempty"`
}

type planConfig struct {
	count               int
	maxLabelsPerState   int
	maxVehicleLegs      int
	maxDuration         time.Duration
	maxTransferDistance int
	originStops         int
	maxExpandedLabels   int
	maxSearchDuration   time.Duration
}

type planRouteNode struct {
	leg       PlanLeg
	parent    *planRouteNode
	depth     int
	signature uint64
}

type planArrival struct {
	at        time.Time
	signature uint64
}

type planLabel struct {
	stop           uint32
	arrival        time.Time
	vehicleLegs    int
	walked         bool
	lastJourney    journeyID
	hasLastJourney bool
	requiredRoute  stringID
	requiredTrip   stringID
	route          *planRouteNode
	index          int
}

type planQueue struct {
	labels            []*planLabel
	data              *graphData
	destinations      []PlanLocation
	heuristicSeconds  []uint32
	corridor          []uint8
	destinationStops  map[uint32]bool
	excludedJourneys  map[string]bool
	maxVehicleLegs    int
	maxLabelsPerState int
	destinationLabels int
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
	priority := label.arrival
	if label.vehicleLegs > 1 {
		// Search useful low-change journeys before exploring routes that happen
		// to reach an intermediate stop slightly earlier through many vehicles.
		// Vehicle-leg count remains in the Pareto state, so higher-change routes
		// are still available when they are genuinely required.
		priority = priority.Add(time.Duration(label.vehicleLegs-1) * planVehicleLegPenalty)
	}
	if q == nil || q.data == nil || int(label.stop) >= len(q.heuristicSeconds) || len(q.destinations) == 0 {
		return priority
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
	return priority.Add(time.Duration(encodedSeconds-1) * time.Second)
}

type planState struct {
	stop           uint32
	vehicleLegs    int
	walked         bool
	lastJourney    journeyID
	hasLastJourney bool
	requiredRoute  stringID
	requiredTrip   stringID
}

func (g *Graph) Plan(ctx context.Context, request PlanRequest) (PlanResponse, error) {
	started := time.Now()
	if g == nil {
		return PlanResponse{}, ErrGraphNotReady
	}
	data := g.current.Load()
	data.mu.RLock()
	defer data.mu.RUnlock()
	if !data.TopologyReady || !data.StaticRoutingReady || len(data.Stops) == 0 || len(data.TransferOffsets) != len(data.Stops)+1 || len(data.JourneyPatterns) != len(data.Journeys) || data.PatternDepartures == nil {
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
	destinationWalks := map[uint32]nearbyStop{}
	destinationRef := "coordinate-destination"
	if request.DestinationLocation != nil {
		destinationRef = fmt.Sprintf("coordinate-destination:%.6f,%.6f", request.DestinationLocation.Longitude, request.DestinationLocation.Latitude)
		for _, nearby := range data.nearbyStops(*request.DestinationLocation, config.maxTransferDistance, config.originStops) {
			destinationNodes[nearby.stop] = true
			destinationWalks[nearby.stop] = nearby
		}
	}
	if len(destinationNodes) == 0 {
		return PlanResponse{Plans: []Plan{}}, nil
	}

	queue := newPlanQueue(data, destinationNodes, request.DestinationLocation)
	queue.corridor = data.planCorridor(destinationNodes, config.maxVehicleLegs)
	queue.maxVehicleLegs = config.maxVehicleLegs
	queue.maxLabelsPerState = config.maxLabelsPerState
	queue.destinationLabels = config.count
	queue.excludedJourneys = make(map[string]bool, len(request.ExcludedJourneyRefs))
	for _, ref := range request.ExcludedJourneyRefs {
		queue.excludedJourneys[ref] = true
	}
	heap.Init(queue)
	best := map[planState][]planArrival{}
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
				walked:  true,
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
		if !currentPlanLabel(queue, best, current) {
			continue
		}
		expanded++
		if destinationNodes[current.stop] && (current.route != nil || request.DestinationLocation != nil) {
			if egress, exists := destinationWalks[current.stop]; exists && egress.distance > 0 {
				duration := int(math.Ceil(egress.distance / planWalkSpeedMetresPerSecond))
				arrival := current.arrival.Add(time.Duration(duration) * time.Second)
				current = &planLabel{stop: current.stop, arrival: arrival, vehicleLegs: current.vehicleLegs, walked: true, route: appendPlanLeg(current.route, PlanLeg{
					Type: ctdf.JourneyPlanRouteItemTypeTransfer, TransferType: ctdf.StopTransferTypeNearbyWalk,
					OriginStopRef: data.stringValue(data.Stops[current.stop].PrimaryRef), DestinationStopRef: destinationRef,
					StartTime: current.arrival, ArrivalTime: arrival, DistanceMetres: int(math.Ceil(egress.distance)),
					WalkDurationSeconds: duration, TotalDurationSeconds: duration,
				})}
			}
			plan := materializePlan(current)
			key := planKey(plan)
			if !resultKeys[key] {
				resultKeys[key] = true
				response.Plans = append(response.Plans, plan)
				if len(response.Plans) == 1 {
					response.FirstPlanMillis = time.Since(started).Milliseconds()
				}
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
	response.ExpandedLabels = expanded
	response.SearchDurationMillis = time.Since(started).Milliseconds()
	log.Info().
		Int("expanded_labels", expanded).
		Int("plans", len(response.Plans)).
		Bool("truncated", response.SearchTruncated).
		Str("truncated_reason", response.SearchTruncatedReason).
		Dur("duration", time.Since(started)).
		Msg("Journey graph plan complete")
	return response, nil
}

func newPlanQueue(data *graphData, destinationNodes map[uint32]bool, destinationLocation ...*PlanLocation) *planQueue {
	queue := &planQueue{data: data, heuristicSeconds: make([]uint32, len(data.Stops)), destinationStops: destinationNodes}
	if len(destinationLocation) > 0 && destinationLocation[0] != nil {
		queue.destinations = append(queue.destinations, *destinationLocation[0])
		return queue
	}
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
	if config.count > 20 {
		config.count = 20
	}
	// FIFO timetable routing only needs the earliest arrival for an ordinary
	// intermediate state: an earlier arrival can wait for every journey a later
	// arrival could board. Requested result count must not multiply the entire
	// search frontier. Destination states retain the requested alternatives.
	config.maxLabelsPerState = defaultPlanLabelsPerState
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

func (d *graphData) expandPlanTransfers(queue *planQueue, best map[planState][]planArrival, config planConfig, current *planLabel, searchEnd time.Time) {
	start, end := d.TransferOffsets[current.stop], d.TransferOffsets[current.stop+1]
	for index := start; index < end; index++ {
		transfer := d.Transfers[index]
		if transfer.DistanceMetres > uint32(config.maxTransferDistance) {
			continue
		}
		transferType := unpackTransferType(transfer.Type)
		equivalence := isEquivalenceTransfer(transferType)
		if current.walked && !equivalence {
			continue
		}
		restrictions := d.applicableTransferRestrictions(uint32(index), current)
		if restrictions == nil {
			continue
		}
		if len(restrictions) == 0 {
			restrictions = []transferRestriction{{}}
		}
		arrival := current.arrival.Add(time.Duration(transfer.TotalDurationSeconds) * time.Second)
		if arrival.After(searchEnd) {
			continue
		}
		for _, restriction := range restrictions {
			pushPlanLabel(queue, best, &planLabel{
				stop: transfer.ToStop, arrival: arrival, vehicleLegs: current.vehicleLegs,
				walked: current.walked || !equivalence, lastJourney: current.lastJourney, hasLastJourney: current.hasLastJourney,
				requiredRoute: restriction.ToRouteRef, requiredTrip: restriction.ToTripRef,
				route: appendPlanLeg(current.route, PlanLeg{
					Type:               ctdf.JourneyPlanRouteItemTypeTransfer,
					TransferType:       transferType,
					OriginStopRef:      d.stringValue(d.Stops[current.stop].PrimaryRef),
					DestinationStopRef: d.stringValue(d.Stops[transfer.ToStop].PrimaryRef),
					StartTime:          current.arrival, ArrivalTime: arrival,
					DistanceMetres: int(transfer.DistanceMetres), WalkDurationSeconds: int(transfer.WalkDurationSeconds), MinChangeDurationSeconds: int(transfer.MinChangeDurationSeconds), TotalDurationSeconds: int(transfer.TotalDurationSeconds),
				}),
			})
		}
	}
}

func isEquivalenceTransfer(value ctdf.StopTransferType) bool {
	switch value {
	case ctdf.StopTransferTypeSameStopGroup, ctdf.StopTransferTypePlatformAlias, ctdf.StopTransferTypeInSeat:
		return true
	default:
		return false
	}
}

func (d *graphData) applicableTransferRestrictions(transfer uint32, current *planLabel) []transferRestriction {
	index := sort.Search(len(d.TransferRestrictions), func(index int) bool {
		return d.TransferRestrictions[index].Transfer >= transfer
	})
	if index >= len(d.TransferRestrictions) || d.TransferRestrictions[index].Transfer != transfer {
		return []transferRestriction{}
	}
	result := make([]transferRestriction, 0, 1)
	for index < len(d.TransferRestrictions) && d.TransferRestrictions[index].Transfer == transfer {
		restriction := d.TransferRestrictions[index]
		index++
		if !current.hasLastJourney || int(current.lastJourney) >= len(d.Journeys) {
			continue
		}
		journey := d.Journeys[current.lastJourney]
		if restriction.FromRouteRef != 0 && restriction.FromRouteRef != journey.ServiceRef {
			continue
		}
		if restriction.FromTripRef != 0 && restriction.FromTripRef != journey.PrimaryID {
			continue
		}
		result = append(result, restriction)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func (d *graphData) expandPlanJourneys(queue *planQueue, best map[planState][]planArrival, config planConfig, current *planLabel, searchEnd time.Time) {
	journeys := map[journeyDayKey]departureEntry{}
	for dayOffset := 0; dayOffset <= 1; dayOffset++ {
		date := current.arrival.AddDate(0, 0, dayOffset)
		day := makeDayKey(date)
		if !d.CompleteDays[day] {
			continue
		}
		serviceDate := dayKeyDate(day, current.arrival.Location())
		aliasStart, aliasEnd := d.StopAliasOffsets[current.stop], d.StopAliasOffsets[current.stop+1]
		for _, alias := range d.StopAliases[aliasStart:aliasEnd] {
			bucket := bucketKey{Day: day, StopRef: alias}
			threshold := int32(current.arrival.Sub(serviceDate) / time.Second)
			for _, pattern := range d.DeparturePatterns[bucket] {
				entries := d.PatternDepartures[patternDepartureKey{Day: day, StopRef: alias, Pattern: pattern}]
				start := sort.Search(len(entries), func(index int) bool { return entries[index].departureSeconds() >= threshold })
				retained := 0
				for _, entry := range entries[start:] {
					candidateDeparture := serviceDate.Add(time.Duration(entry.departureSeconds()) * time.Second)
					if candidateDeparture.After(searchEnd) {
						break
					}
					if int(entry.journey()) >= len(d.Journeys) {
						continue
					}
					record := d.Journeys[entry.journey()]
					journeyRef := d.stringValue(record.PrimaryID)
					if queue.excludedJourneys[journeyRef] || current.requiredRoute != 0 && current.requiredRoute != record.ServiceRef || current.requiredTrip != 0 && current.requiredTrip != record.PrimaryID {
						continue
					}
					key := journeyDayKey{Day: day, Journey: entry.journey()}
					if existing, exists := journeys[key]; !exists || entry.departureSeconds() < existing.departureSeconds() {
						journeys[key] = entry
					}
					retained++
					if retained >= max(1, config.count) {
						break
					}
				}
			}
		}
	}
	for active, boarding := range journeys {
		if int(active.Journey) >= len(d.Journeys) {
			continue
		}
		record := d.Journeys[active.Journey]
		if current.requiredRoute != 0 && current.requiredRoute != record.ServiceRef {
			continue
		}
		if current.requiredTrip != 0 && current.requiredTrip != record.PrimaryID {
			continue
		}
		journeyRef := d.stringValue(record.PrimaryID)
		if queue.excludedJourneys[journeyRef] {
			continue
		}
		// A ride expansion already reaches every downstream alighting stop.
		// Re-boarding the same vehicle at its next stop only creates segmented
		// duplicates of that ride and incorrectly consumes another change.
		if current.route != nil && current.route.leg.Type == ctdf.JourneyPlanRouteItemTypeJourney && current.route.leg.JourneyRef == journeyRef {
			continue
		}
		serviceDate := dayKeyDate(active.Day, current.arrival.Location())
		boardingIndex := int(boarding.pathIndex())
		if boardingIndex < 0 || boardingIndex >= int(record.PathCount) {
			continue
		}
		departure := serviceDate.Add(time.Duration(boarding.departureSeconds()) * time.Second)
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
			pushPlanLabel(queue, best, &planLabel{stop: stop, arrival: arrival, vehicleLegs: current.vehicleLegs + 1, lastJourney: active.Journey, hasLastJourney: true, route: appendPlanLeg(current.route, PlanLeg{
				Type:                        ctdf.JourneyPlanRouteItemTypeJourney,
				JourneyRef:                  journeyRef,
				JourneyOriginStopIndex:      boardingIndex,
				JourneyDestinationStopIndex: index + 1,
				OriginStopRef:               d.stringValue(d.Paths[record.PathStart+uint32(boardingIndex)].OriginStopRef),
				DestinationStopRef:          d.stringValue(path.DestinationStopRef),
				StartTime:                   departure, ArrivalTime: arrival,
			})})
		}
	}
}

func pushPlanLabel(queue *planQueue, best map[planState][]planArrival, label *planLabel) bool {
	if queue != nil && len(queue.corridor) > 0 {
		remainingVehicleLegs := queue.maxVehicleLegs - label.vehicleLegs
		if remainingVehicleLegs < 0 || int(label.stop) >= len(queue.corridor) || queue.corridor[label.stop] > uint8(remainingVehicleLegs) {
			return false
		}
	}
	state := stateForPlanLabel(queue, label)
	arrivals := best[state]
	signature := uint64(0)
	if label.route != nil {
		signature = label.route.signature
	}
	for _, arrival := range arrivals {
		if arrival.at.Equal(label.arrival) && arrival.signature == signature {
			return false
		}
	}
	limit := defaultPlanCount
	if queue != nil {
		limit = queue.maxLabelsPerState
		if queue.destinationStops[label.stop] {
			limit = queue.destinationLabels
		}
	}
	if limit <= 0 {
		limit = defaultPlanCount
	}
	if len(arrivals) >= limit {
		last := len(arrivals) - 1
		if !label.arrival.Before(arrivals[last].at) {
			return false
		}
		arrivals[last] = planArrival{at: label.arrival, signature: signature}
	} else {
		arrivals = append(arrivals, planArrival{at: label.arrival, signature: signature})
	}
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].at.Before(arrivals[j].at) })
	best[state] = arrivals
	heap.Push(queue, label)
	return true
}

func currentPlanLabel(queue *planQueue, best map[planState][]planArrival, label *planLabel) bool {
	signature := uint64(0)
	if label.route != nil {
		signature = label.route.signature
	}
	for _, arrival := range best[stateForPlanLabel(queue, label)] {
		if arrival.at.Equal(label.arrival) && arrival.signature == signature {
			return true
		}
	}
	return false
}

func stateForPlanLabel(queue *planQueue, label *planLabel) planState {
	lastJourney := label.lastJourney
	hasLastJourney := label.hasLastJourney
	if queue != nil && queue.data != nil && int(label.stop) < len(queue.data.IncomingJourneyStateStops) && !queue.data.IncomingJourneyStateStops[label.stop] {
		// The incoming journey only affects which route/trip-restricted transfer
		// can be used next. At ordinary stops it must not split an otherwise
		// identical state by every service that happened to arrive there.
		lastJourney = 0
		hasLastJourney = false
	}
	return planState{
		stop: label.stop, vehicleLegs: label.vehicleLegs, walked: label.walked,
		lastJourney: lastJourney, hasLastJourney: hasLastJourney,
		requiredRoute: label.requiredRoute, requiredTrip: label.requiredTrip,
	}
}

func appendPlanLeg(parent *planRouteNode, leg PlanLeg) *planRouteNode {
	depth := 1
	signature := uint64(1469598103934665603)
	if parent != nil {
		depth = parent.depth + 1
		signature = parent.signature
	}
	for _, value := range []string{string(leg.Type), leg.JourneyRef, string(leg.TransferType), leg.OriginStopRef, leg.DestinationStopRef} {
		for index := 0; index < len(value); index++ {
			signature ^= uint64(value[index])
			signature *= 1099511628211
		}
		signature ^= 0xff
		signature *= 1099511628211
	}
	return &planRouteNode{leg: leg, parent: parent, depth: depth, signature: signature}
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
