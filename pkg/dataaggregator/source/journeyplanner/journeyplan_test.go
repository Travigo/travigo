package journeyplanner

import (
	"testing"
	"time"

	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator/query"
)

func TestJourneyPlanConfigDefaultsBoundSearchFanout(t *testing.T) {
	config := journeyPlanConfig(query.JourneyPlan{})

	if config.count != defaultJourneyPlanCount {
		t.Fatalf("expected default count %d, got %d", defaultJourneyPlanCount, config.count)
	}
	if config.departureBoardCount != defaultJourneyPlanDepartureBoardCount {
		t.Fatalf("expected default departure board count %d, got %d", defaultJourneyPlanDepartureBoardCount, config.departureBoardCount)
	}
	if config.originDepartureBoardCount != defaultJourneyPlanOriginDepartureBoardCount {
		t.Fatalf("expected default origin departure board count %d, got %d", defaultJourneyPlanOriginDepartureBoardCount, config.originDepartureBoardCount)
	}
	if config.originLocationStopCount != defaultJourneyPlanOriginLocationStopCount {
		t.Fatalf("expected default origin location stop count %d, got %d", defaultJourneyPlanOriginLocationStopCount, config.originLocationStopCount)
	}
	if config.maxExpandedLabels != defaultJourneyPlanMaxExpandedLabels {
		t.Fatalf("expected default max expanded labels %d, got %d", defaultJourneyPlanMaxExpandedLabels, config.maxExpandedLabels)
	}
	if config.maxSearchDuration != defaultJourneyPlanMaxSearchDuration {
		t.Fatalf("expected default max search duration %s, got %s", defaultJourneyPlanMaxSearchDuration, config.maxSearchDuration)
	}
}

func TestJourneyPlanConfigAllowsSearchBudgetOverrides(t *testing.T) {
	config := journeyPlanConfig(query.JourneyPlan{
		Count:                      7,
		DepartureBoardCountPerStop: 20,
		OriginDepartureBoardCount:  50,
		OriginLocationStopCount:    9,
		MaxExpandedLabels:          80,
		MaxSearchDuration:          3 * time.Second,
	})

	if config.count != 7 {
		t.Fatalf("expected count override 7, got %d", config.count)
	}
	if config.departureBoardCount != 20 {
		t.Fatalf("expected departure board count override 20, got %d", config.departureBoardCount)
	}
	if config.originDepartureBoardCount != 50 {
		t.Fatalf("expected origin departure board count override 50, got %d", config.originDepartureBoardCount)
	}
	if config.originLocationStopCount != 9 {
		t.Fatalf("expected origin location stop count override 9, got %d", config.originLocationStopCount)
	}
	if config.maxExpandedLabels != 80 {
		t.Fatalf("expected max expanded labels override 80, got %d", config.maxExpandedLabels)
	}
	if config.maxSearchDuration != 3*time.Second {
		t.Fatalf("expected max search duration override 3s, got %s", config.maxSearchDuration)
	}
}

func TestLimitDepartureBoardSortsAndLimits(t *testing.T) {
	start := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	board := []*ctdf.DepartureBoard{
		{Time: start.Add(20 * time.Minute)},
		nil,
		{Time: start.Add(5 * time.Minute)},
		{Time: start.Add(10 * time.Minute)},
	}

	limited := limitDepartureBoard(board, 2)

	if len(limited) != 2 {
		t.Fatalf("expected 2 departures, got %d", len(limited))
	}
	if !limited[0].Time.Equal(start.Add(5 * time.Minute)) {
		t.Fatalf("expected first departure at +5m, got %s", limited[0].Time)
	}
	if !limited[1].Time.Equal(start.Add(10 * time.Minute)) {
		t.Fatalf("expected second departure at +10m, got %s", limited[1].Time)
	}
}

func TestLoadDepartureBoardReusesFullGeneratedBoardForLaterArrival(t *testing.T) {
	start := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	lookupCalls := 0
	runtime := &plannerRuntime{
		departureBoardCache: map[string]cachedDepartureBoard{},
		departureBoardLookup: func(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
			lookupCalls++
			board := make([]*ctdf.DepartureBoard, 10)
			for index := range board {
				board[index] = &ctdf.DepartureBoard{Time: start.Add(time.Duration(index) * time.Minute)}
			}
			return board, nil
		},
	}
	stop := &ctdf.Stop{PrimaryIdentifier: "interchange"}

	first, err := runtime.loadDepartureBoard(stop, start, 3)
	if err != nil {
		t.Fatalf("first board lookup failed: %s", err)
	}
	later, err := runtime.loadDepartureBoard(stop, start.Add(5*time.Minute), 3)
	if err != nil {
		t.Fatalf("later board lookup failed: %s", err)
	}

	if lookupCalls != 1 {
		t.Fatalf("expected one underlying board lookup, got %d", lookupCalls)
	}
	if len(first) != 3 || !first[0].Time.Equal(start) || !first[2].Time.Equal(start.Add(2*time.Minute)) {
		t.Fatalf("unexpected first board: %+v", first)
	}
	if len(later) != 3 || !later[0].Time.Equal(start.Add(5*time.Minute)) || !later[2].Time.Equal(start.Add(7*time.Minute)) {
		t.Fatalf("expected cached board to advance to +5m..+7m, got %+v", later)
	}
}

func TestExpandTransfersDefersDepartureBoardLookupUntilQueuedLabelIsExpanded(t *testing.T) {
	start := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	fromStop := &ctdf.Stop{PrimaryIdentifier: "bus-stop"}
	toStop := &ctdf.Stop{PrimaryIdentifier: "rail-station", TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	departureBoardLookups := 0
	runtime := &plannerRuntime{
		stopCache: map[string]*ctdf.Stop{
			toStop.PrimaryIdentifier: toStop,
		},
		transferCache: map[string][]*ctdf.StopTransfer{
			fromStop.PrimaryIdentifier: {
				{
					FromStopRef:          fromStop.PrimaryIdentifier,
					ToStopRef:            toStop.PrimaryIdentifier,
					Type:                 ctdf.StopTransferTypeNearbyWalk,
					TotalDurationSeconds: 120,
				},
			},
		},
		departureBoardCache: map[string]cachedDepartureBoard{},
		departureBoardLookup: func(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
			departureBoardLookups++
			return nil, nil
		},
		bestArrivals: map[plannerStateKey][]time.Time{},
		resultKeys:   map[string]bool{},
		config: plannerConfig{
			count:               1,
			maxVehicleLegs:      2,
			maxTransferDistance: 1000,
			maxLabelsPerState:   1,
		},
		searchEndTime: start.Add(time.Hour),
	}
	pq := &plannerPriorityQueue{}

	err := runtime.expandTransfers(pq, &plannerLabel{
		stop:        fromStop,
		arrivalTime: start,
	}, &ctdf.Stop{PrimaryIdentifier: "destination"}, &ctdf.JourneyPlanResults{})
	if err != nil {
		t.Fatalf("transfer expansion failed: %s", err)
	}

	if departureBoardLookups != 0 {
		t.Fatalf("expected transfer expansion to defer board lookup, got %d lookups", departureBoardLookups)
	}
	if pq.Len() != 1 || (*pq)[0].stop != toStop {
		t.Fatalf("expected rail transfer label to be queued, got %+v", *pq)
	}
}

func TestExpandTransfersFindsDirectAndConnectingRoutesViaAccessRailInterchange(t *testing.T) {
	start := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	cambridgeLocation := &ctdf.Location{Type: "Point", Coordinates: []float64{0.137, 52.194}}
	londonLocation := &ctdf.Location{Type: "Point", Coordinates: []float64{-0.123, 51.531}}

	busStop := &ctdf.Stop{PrimaryIdentifier: "bus-stop", Location: cambridgeLocation}
	cambridge := &ctdf.Stop{
		PrimaryIdentifier: "cambridge",
		Location:          cambridgeLocation,
		TransportTypes:    []ctdf.TransportType{ctdf.TransportTypeRail},
	}
	kingsCross := &ctdf.Stop{PrimaryIdentifier: "kings-cross", Location: londonLocation}
	stPancras := &ctdf.Stop{
		PrimaryIdentifier: "st-pancras",
		Location:          &ctdf.Location{Type: "Point", Coordinates: []float64{-0.126, 51.530}},
		TransportTypes:    []ctdf.TransportType{ctdf.TransportTypeRail},
	}
	blackfriars := &ctdf.Stop{
		PrimaryIdentifier: "blackfriars",
		Location:          &ctdf.Location{Type: "Point", Coordinates: []float64{-0.103, 51.512}},
		TransportTypes:    []ctdf.TransportType{ctdf.TransportTypeRail},
	}

	boardLookups := []string{}
	runtime := &plannerRuntime{
		stopCache: map[string]*ctdf.Stop{
			cambridge.PrimaryIdentifier:   cambridge,
			kingsCross.PrimaryIdentifier:  kingsCross,
			stPancras.PrimaryIdentifier:   stPancras,
			blackfriars.PrimaryIdentifier: blackfriars,
		},
		transferCache: map[string][]*ctdf.StopTransfer{
			busStop.PrimaryIdentifier: {
				{
					FromStopRef:          busStop.PrimaryIdentifier,
					ToStopRef:            cambridge.PrimaryIdentifier,
					Type:                 ctdf.StopTransferTypeNearbyWalk,
					TotalDurationSeconds: 120,
				},
			},
			kingsCross.PrimaryIdentifier: {
				{
					FromStopRef:          kingsCross.PrimaryIdentifier,
					ToStopRef:            stPancras.PrimaryIdentifier,
					Type:                 ctdf.StopTransferTypeNearbyWalk,
					TotalDurationSeconds: 300,
				},
			},
		},
		departureBoardCache: map[string]cachedDepartureBoard{},
		departureBoardLookup: func(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
			boardLookups = append(boardLookups, q.Stop.PrimaryIdentifier)
			switch q.Stop.PrimaryIdentifier {
			case cambridge.PrimaryIdentifier:
				return []*ctdf.DepartureBoard{
					{
						Time: start.Add(5 * time.Minute),
						Journey: &ctdf.Journey{
							PrimaryIdentifier: "cambridge-kings-cross",
							Path: []*ctdf.JourneyPathItem{
								{
									OriginStopRef:          cambridge.PrimaryIdentifier,
									DestinationStopRef:     kingsCross.PrimaryIdentifier,
									OriginDepartureTime:    start.Add(5 * time.Minute),
									DestinationArrivalTime: start.Add(65 * time.Minute),
								},
							},
						},
					},
					{
						Time: start.Add(10 * time.Minute),
						Journey: &ctdf.Journey{
							PrimaryIdentifier: "cambridge-brighton",
							Path: []*ctdf.JourneyPathItem{
								{
									OriginStopRef:          cambridge.PrimaryIdentifier,
									DestinationStopRef:     blackfriars.PrimaryIdentifier,
									OriginDepartureTime:    start.Add(10 * time.Minute),
									DestinationArrivalTime: start.Add(70 * time.Minute),
								},
								{
									OriginStopRef:          blackfriars.PrimaryIdentifier,
									DestinationStopRef:     "brighton",
									OriginDepartureTime:    start.Add(72 * time.Minute),
									DestinationArrivalTime: start.Add(130 * time.Minute),
								},
							},
						},
					},
				}, nil
			case stPancras.PrimaryIdentifier:
				return []*ctdf.DepartureBoard{
					{
						Time: start.Add(75 * time.Minute),
						Journey: &ctdf.Journey{
							PrimaryIdentifier: "st-pancras-blackfriars",
							Path: []*ctdf.JourneyPathItem{
								{
									OriginStopRef:          stPancras.PrimaryIdentifier,
									DestinationStopRef:     blackfriars.PrimaryIdentifier,
									OriginDepartureTime:    start.Add(75 * time.Minute),
									DestinationArrivalTime: start.Add(85 * time.Minute),
								},
							},
						},
					},
				}, nil
			default:
				return nil, nil
			}
		},
		bestArrivals: map[plannerStateKey][]time.Time{},
		resultKeys:   map[string]bool{},
		config: plannerConfig{
			count:                   5,
			maxVehicleLegs:          4,
			maxTransferDistance:     1000,
			departureBoardCount:     12,
			maxRouteItems:           10,
			maxConsecutiveTransfers: 1,
			maxLabelsPerState:       5,
		},
		searchEndTime: start.Add(6 * time.Hour),
	}
	pq := &plannerPriorityQueue{}
	results := &ctdf.JourneyPlanResults{}
	current := &plannerLabel{
		stop:        busStop,
		arrivalTime: start,
		vehicleLegs: 1,
		routeItems: appendRouteItem(nil, ctdf.JourneyPlanRouteItem{
			Type:               ctdf.JourneyPlanRouteItemTypeJourney,
			OriginStopRef:      "origin",
			DestinationStopRef: busStop.PrimaryIdentifier,
			StartTime:          start.Add(-30 * time.Minute),
			ArrivalTime:        start,
			Journey:            &ctdf.Journey{PrimaryIdentifier: "access-bus"},
		}),
	}

	if err := runtime.expandTransfers(pq, current, blackfriars, results); err != nil {
		t.Fatalf("transfer expansion failed: %s", err)
	}

	if len(results.JourneyPlans) != 2 {
		t.Fatalf("expected direct Brighton and connecting journey plans, got %d", len(results.JourneyPlans))
	}
	if len(boardLookups) != 2 || boardLookups[0] != cambridge.PrimaryIdentifier || boardLookups[1] != stPancras.PrimaryIdentifier {
		t.Fatalf("expected bounded Cambridge and St Pancras board lookups, got %v", boardLookups)
	}
	var directRouteItems []ctdf.JourneyPlanRouteItem
	var connectingRouteItems []ctdf.JourneyPlanRouteItem
	for _, plan := range results.JourneyPlans {
		switch len(plan.RouteItems) {
		case 3:
			directRouteItems = plan.RouteItems
		case 5:
			connectingRouteItems = plan.RouteItems
		}
	}
	if len(directRouteItems) != 3 || directRouteItems[2].Journey == nil || directRouteItems[2].Journey.PrimaryIdentifier != "cambridge-brighton" {
		t.Fatalf("expected the Brighton service to provide the direct Blackfriars route, got %+v", directRouteItems)
	}
	if len(connectingRouteItems) != 5 {
		t.Fatalf("expected access, transfer, rail, transfer, rail route items, got %+v", connectingRouteItems)
	}
	if connectingRouteItems[2].OriginStopRef != cambridge.PrimaryIdentifier || connectingRouteItems[2].DestinationStopRef != kingsCross.PrimaryIdentifier {
		t.Fatalf("unexpected main rail leg %s -> %s", connectingRouteItems[2].OriginStopRef, connectingRouteItems[2].DestinationStopRef)
	}
	if connectingRouteItems[4].OriginStopRef != stPancras.PrimaryIdentifier || connectingRouteItems[4].DestinationStopRef != blackfriars.PrimaryIdentifier {
		t.Fatalf("unexpected final rail leg %s -> %s", connectingRouteItems[4].OriginStopRef, connectingRouteItems[4].DestinationStopRef)
	}
}

func TestReplacedLabelIsNoLongerCurrent(t *testing.T) {
	start := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	stop := &ctdf.Stop{PrimaryIdentifier: "interchange"}
	runtime := &plannerRuntime{
		bestArrivals:  map[plannerStateKey][]time.Time{},
		config:        plannerConfig{maxLabelsPerState: 1},
		searchEndTime: start.Add(time.Hour),
	}
	pq := &plannerPriorityQueue{}
	later := &plannerLabel{stop: stop, arrivalTime: start.Add(10 * time.Minute)}
	earlier := &plannerLabel{stop: stop, arrivalTime: start.Add(5 * time.Minute)}

	if !runtime.pushLabel(pq, later) || !runtime.pushLabel(pq, earlier) {
		t.Fatal("expected both labels to be accepted before the later one became stale")
	}
	if runtime.isCurrentLabel(later) {
		t.Fatal("expected replaced later label to be stale")
	}
	if !runtime.isCurrentLabel(earlier) {
		t.Fatal("expected earlier replacement label to remain current")
	}
}

func TestRecordResultBuildsAndDeduplicatesPlan(t *testing.T) {
	start := time.Date(2026, 7, 7, 19, 39, 0, 0, time.UTC)
	arrival := start.Add(65 * time.Minute)
	runtime := &plannerRuntime{
		resultKeys: map[string]bool{},
	}
	results := &ctdf.JourneyPlanResults{}
	label := &plannerLabel{
		stop:        &ctdf.Stop{PrimaryIdentifier: "destination"},
		arrivalTime: arrival,
		routeItems: appendRouteItem(nil, ctdf.JourneyPlanRouteItem{
			Type:               ctdf.JourneyPlanRouteItemTypeJourney,
			OriginStopRef:      "origin",
			DestinationStopRef: "destination",
			StartTime:          start,
			ArrivalTime:        arrival,
			Journey:            &ctdf.Journey{PrimaryIdentifier: "journey-1"},
		}),
	}

	runtime.recordResult(results, label)
	runtime.recordResult(results, label)

	if len(results.JourneyPlans) != 1 {
		t.Fatalf("expected one deduplicated journey plan, got %d", len(results.JourneyPlans))
	}
	plan := results.JourneyPlans[0]
	if !plan.StartTime.Equal(start) {
		t.Fatalf("expected plan start %s, got %s", start, plan.StartTime)
	}
	if !plan.ArrivalTime.Equal(arrival) {
		t.Fatalf("expected plan arrival %s, got %s", arrival, plan.ArrivalTime)
	}
	if len(plan.RouteItems) != 1 {
		t.Fatalf("expected one route item, got %d", len(plan.RouteItems))
	}
}

func TestRecordDirectDestinationFromDeparture(t *testing.T) {
	start := time.Date(2026, 7, 7, 20, 25, 0, 0, time.UTC)
	arrival := start.Add(87 * time.Minute)
	runtime := &plannerRuntime{
		resultKeys:    map[string]bool{},
		searchEndTime: start.Add(6 * time.Hour),
	}
	results := &ctdf.JourneyPlanResults{}
	current := &plannerLabel{
		stop:        &ctdf.Stop{PrimaryIdentifier: "origin"},
		arrivalTime: start.Add(-10 * time.Minute),
	}
	departure := &ctdf.DepartureBoard{
		Time: start,
		Journey: &ctdf.Journey{
			PrimaryIdentifier: "journey-1",
			Path: []*ctdf.JourneyPathItem{
				{
					OriginStopRef:          "origin",
					DestinationStopRef:     "middle",
					DestinationArrivalTime: start.Add(20 * time.Minute),
					OriginDepartureTime:    start,
					DestinationActivity:    []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown},
					OriginActivity:         []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup},
				},
				{
					OriginStopRef:          "middle",
					DestinationStopRef:     "destination",
					DestinationArrivalTime: arrival,
					OriginDepartureTime:    start.Add(25 * time.Minute),
					DestinationActivity:    []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivitySetdown},
					OriginActivity:         []ctdf.JourneyPathItemActivity{ctdf.JourneyPathItemActivityPickup},
				},
			},
		},
	}

	recorded := runtime.recordDirectDestinationFromDeparture(current, departure, 0, &ctdf.Stop{PrimaryIdentifier: "destination"}, results)

	if !recorded {
		t.Fatal("expected direct destination to be recorded")
	}
	if len(results.JourneyPlans) != 1 {
		t.Fatalf("expected one journey plan, got %d", len(results.JourneyPlans))
	}
	routeItems := results.JourneyPlans[0].RouteItems
	if len(routeItems) != 1 {
		t.Fatalf("expected one route item, got %d", len(routeItems))
	}
	if routeItems[0].OriginStopRef != "origin" || routeItems[0].DestinationStopRef != "destination" {
		t.Fatalf("unexpected route item %s -> %s", routeItems[0].OriginStopRef, routeItems[0].DestinationStopRef)
	}
	if !routeItems[0].ArrivalTime.Equal(arrival) {
		t.Fatalf("expected arrival %s, got %s", arrival, routeItems[0].ArrivalTime)
	}
}

func TestCoordinateOriginStop(t *testing.T) {
	location := &ctdf.Location{
		Type:        "Point",
		Coordinates: []float64{-0.1234567, 52.1234567},
	}

	stop := coordinateOriginStop(location)

	if stop.PrimaryIdentifier != "coordinate-origin:-0.123457,52.123457" {
		t.Fatalf("unexpected coordinate origin identifier %q", stop.PrimaryIdentifier)
	}
	if stop.PrimaryName != "Selected location" {
		t.Fatalf("unexpected coordinate origin name %q", stop.PrimaryName)
	}
	if stop.Location == nil || len(stop.Location.Coordinates) != 2 {
		t.Fatal("expected coordinate origin location to be set")
	}
	if stop.Location.Coordinates[0] != location.Coordinates[0] || stop.Location.Coordinates[1] != location.Coordinates[1] {
		t.Fatalf("unexpected coordinate origin location %+v", stop.Location.Coordinates)
	}
}

func TestOriginLocationLabelBuildsInitialWalkTransfer(t *testing.T) {
	start := time.Date(2026, 7, 7, 8, 0, 0, 0, time.UTC)
	originStop := &ctdf.Stop{PrimaryIdentifier: "coordinate-origin:-0.100000,52.000000"}
	originLocation := &ctdf.Location{
		Type:        "Point",
		Coordinates: []float64{-0.100000, 52.000000},
	}
	stop := &ctdf.Stop{
		PrimaryIdentifier: "nearby-stop",
		Location: &ctdf.Location{
			Type:        "Point",
			Coordinates: []float64{-0.100000, 52.001000},
		},
	}

	label := originLocationLabel(originStop, originLocation, stop, start)

	if label == nil {
		t.Fatal("expected origin location label")
	}
	if label.stop != stop {
		t.Fatal("expected label to arrive at nearby stop")
	}
	routeItems := routeItemsSlice(label.routeItems)
	if len(routeItems) != 1 {
		t.Fatalf("expected one initial route item, got %d", len(routeItems))
	}
	routeItem := routeItems[0]
	if routeItem.Type != ctdf.JourneyPlanRouteItemTypeTransfer {
		t.Fatalf("expected transfer route item, got %s", routeItem.Type)
	}
	if routeItem.TransferType != ctdf.StopTransferTypeNearbyWalk {
		t.Fatalf("expected nearby walk transfer, got %s", routeItem.TransferType)
	}
	if routeItem.OriginStopRef != originStop.PrimaryIdentifier || routeItem.DestinationStopRef != stop.PrimaryIdentifier {
		t.Fatalf("unexpected transfer refs %s -> %s", routeItem.OriginStopRef, routeItem.DestinationStopRef)
	}
	if routeItem.DistanceMetres <= 0 || routeItem.WalkDurationSeconds <= 0 {
		t.Fatalf("expected positive walk distance and duration, got %dm/%ds", routeItem.DistanceMetres, routeItem.WalkDurationSeconds)
	}
	if !routeItem.ArrivalTime.Equal(label.arrivalTime) {
		t.Fatalf("expected route item arrival %s to match label arrival %s", routeItem.ArrivalTime, label.arrivalTime)
	}
}
