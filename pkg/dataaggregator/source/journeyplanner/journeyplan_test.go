package journeyplanner

import (
	"container/heap"
	"fmt"
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

func TestExpandTransfersOnlyPromotesHighCapacityTargets(t *testing.T) {
	start := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	fromStop := &ctdf.Stop{PrimaryIdentifier: "origin"}
	busStop := &ctdf.Stop{PrimaryIdentifier: "nearby-bus", TransportTypes: []ctdf.TransportType{ctdf.TransportTypeBus}}
	railStop := &ctdf.Stop{PrimaryIdentifier: "rail-interchange", TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	destination := &ctdf.Stop{PrimaryIdentifier: "destination", TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	runtime := &plannerRuntime{
		stopCache: map[string]*ctdf.Stop{
			busStop.PrimaryIdentifier:  busStop,
			railStop.PrimaryIdentifier: railStop,
		},
		transferCache: map[string][]*ctdf.StopTransfer{
			fromStop.PrimaryIdentifier: {
				{FromStopRef: fromStop.PrimaryIdentifier, ToStopRef: busStop.PrimaryIdentifier, Type: ctdf.StopTransferTypeNearbyWalk, TotalDurationSeconds: 60},
				{FromStopRef: fromStop.PrimaryIdentifier, ToStopRef: railStop.PrimaryIdentifier, Type: ctdf.StopTransferTypeNearbyWalk, TotalDurationSeconds: 120},
			},
		},
		departureBoardCache: map[string]cachedDepartureBoard{},
		bestArrivals:        map[plannerStateKey][]time.Time{},
		resultKeys:          map[string]bool{},
		config: plannerConfig{
			maxVehicleLegs:      2,
			maxTransferDistance: 1000,
			maxLabelsPerState:   1,
			maxRouteItems:       6,
		},
		searchEndTime:   start.Add(time.Hour),
		destinationStop: destination,
	}
	pq := &plannerPriorityQueue{}
	if err := runtime.expandTransfers(pq, &plannerLabel{stop: fromStop, arrivalTime: start}, destination, &ctdf.JourneyPlanResults{}); err != nil {
		t.Fatalf("expand transfers: %v", err)
	}
	if pq.Len() != 2 {
		t.Fatalf("queued transfers = %d, want 2", pq.Len())
	}
	for _, label := range *pq {
		switch label.stop.PrimaryIdentifier {
		case busStop.PrimaryIdentifier:
			if label.preferExpansion || label.priorityClass == 0 {
				t.Fatalf("nearby bus transfer was promoted: %+v", label)
			}
		case railStop.PrimaryIdentifier:
			if !label.preferExpansion || label.priorityClass != 0 {
				t.Fatalf("rail transfer was not promoted: %+v", label)
			}
		}
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
			count:                   2,
			maxVehicleLegs:          4,
			maxTransferDistance:     1000,
			departureBoardCount:     12,
			maxRouteItems:           10,
			maxConsecutiveTransfers: 1,
			maxLabelsPerState:       2,
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
	for pq.Len() > 0 && len(results.JourneyPlans) < 2 {
		label := heap.Pop(pq).(*plannerLabel)
		if !runtime.isCurrentLabel(label) {
			continue
		}
		if !label.transfersExpanded && label.consecutiveTransfers < runtime.config.maxConsecutiveTransfers {
			if err := runtime.expandTransfers(pq, label, blackfriars, results); err != nil {
				t.Fatalf("queued transfer expansion failed: %s", err)
			}
		}
		if len(results.JourneyPlans) >= runtime.config.count {
			continue
		}
		if !label.departuresExpanded && label.vehicleLegs < runtime.config.maxVehicleLegs {
			if err := runtime.expandDepartures(pq, label, blackfriars, results); err != nil {
				t.Fatalf("queued departure expansion failed: %s", err)
			}
		}
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

func TestExpandDeparturesDefersDownstreamTransfersUntilLabelIsPopped(t *testing.T) {
	start := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	origin := &ctdf.Stop{PrimaryIdentifier: "origin"}
	intermediate := &ctdf.Stop{PrimaryIdentifier: "intermediate"}
	transferTarget := &ctdf.Stop{PrimaryIdentifier: "transfer-target"}
	destination := &ctdf.Stop{PrimaryIdentifier: "destination"}
	runtime := &plannerRuntime{
		stopCache: map[string]*ctdf.Stop{
			origin.PrimaryIdentifier:         origin,
			intermediate.PrimaryIdentifier:   intermediate,
			transferTarget.PrimaryIdentifier: transferTarget,
			destination.PrimaryIdentifier:    destination,
		},
		transferCache: map[string][]*ctdf.StopTransfer{
			intermediate.PrimaryIdentifier: {
				{
					FromStopRef:          intermediate.PrimaryIdentifier,
					ToStopRef:            transferTarget.PrimaryIdentifier,
					Type:                 ctdf.StopTransferTypeNearbyWalk,
					TotalDurationSeconds: 60,
				},
			},
		},
		departureBoardCache: map[string]cachedDepartureBoard{},
		departureBoardLookup: func(query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
			return []*ctdf.DepartureBoard{{
				Time: start.Add(time.Minute),
				Journey: &ctdf.Journey{
					PrimaryIdentifier: "origin-intermediate",
					Path: []*ctdf.JourneyPathItem{{
						OriginStopRef:          origin.PrimaryIdentifier,
						DestinationStopRef:     intermediate.PrimaryIdentifier,
						OriginDepartureTime:    start.Add(time.Minute),
						DestinationArrivalTime: start.Add(10 * time.Minute),
					}},
				},
			}}, nil
		},
		bestArrivals: map[plannerStateKey][]time.Time{},
		resultKeys:   map[string]bool{},
		config: plannerConfig{
			count:                     1,
			maxVehicleLegs:            2,
			originDepartureBoardCount: 1,
			maxTransferDistance:       1000,
			maxConsecutiveTransfers:   1,
			maxLabelsPerState:         1,
			maxRouteItems:             6,
		},
		searchEndTime:   start.Add(time.Hour),
		destinationStop: destination,
	}
	pq := &plannerPriorityQueue{}
	current := &plannerLabel{stop: origin, arrivalTime: start}
	results := &ctdf.JourneyPlanResults{}

	if err := runtime.expandDepartures(pq, current, destination, results); err != nil {
		t.Fatalf("departure expansion failed: %s", err)
	}
	if pq.Len() != 1 || (*pq)[0].stop != intermediate || (*pq)[0].transfersExpanded {
		t.Fatalf("downstream transfer expanded eagerly: %+v", *pq)
	}

	queued := heap.Pop(pq).(*plannerLabel)
	if err := runtime.expandTransfers(pq, queued, destination, results); err != nil {
		t.Fatalf("lazy transfer expansion failed: %s", err)
	}
	if pq.Len() != 1 || (*pq)[0].stop != transferTarget {
		t.Fatalf("expected transfer target after queued label expansion, got %+v", *pq)
	}
}

func TestCoordinateOriginAccessUsesBoundedPerStopDepartureCount(t *testing.T) {
	start := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	stop := &ctdf.Stop{PrimaryIdentifier: "nearby-stop"}
	requestedCount := 0
	runtime := &plannerRuntime{
		stopCache:           map[string]*ctdf.Stop{stop.PrimaryIdentifier: stop},
		transferCache:       map[string][]*ctdf.StopTransfer{},
		departureBoardCache: map[string]cachedDepartureBoard{},
		bestArrivals:        map[plannerStateKey][]time.Time{},
		resultKeys:          map[string]bool{},
		departureBoardLookup: func(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
			requestedCount = q.Count
			return nil, nil
		},
		config: plannerConfig{
			count:                     1,
			maxVehicleLegs:            2,
			departureBoardCount:       12,
			originDepartureBoardCount: 96,
			maxLabelsPerState:         1,
		},
		searchEndTime: start.Add(time.Hour),
	}
	current := &plannerLabel{
		stop:        stop,
		arrivalTime: start,
		routeItems: appendRouteItem(nil, ctdf.JourneyPlanRouteItem{
			Type:               ctdf.JourneyPlanRouteItemTypeTransfer,
			DestinationStopRef: stop.PrimaryIdentifier,
		}),
	}
	if err := runtime.expandDepartures(&plannerPriorityQueue{}, current, &ctdf.Stop{PrimaryIdentifier: "destination"}, &ctdf.JourneyPlanResults{}); err != nil {
		t.Fatalf("coordinate-origin departure expansion failed: %s", err)
	}
	if requestedCount != 12 {
		t.Fatalf("coordinate-origin board count = %d, want 12", requestedCount)
	}
}

func TestExpandDeparturesKeepsDirectResultWhenLookupExhaustsBudget(t *testing.T) {
	start := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	origin := &ctdf.Stop{PrimaryIdentifier: "origin"}
	destination := &ctdf.Stop{PrimaryIdentifier: "destination"}
	runtime := &plannerRuntime{
		stopCache:           map[string]*ctdf.Stop{origin.PrimaryIdentifier: origin, destination.PrimaryIdentifier: destination},
		transferCache:       map[string][]*ctdf.StopTransfer{},
		departureBoardCache: map[string]cachedDepartureBoard{},
		bestArrivals:        map[plannerStateKey][]time.Time{},
		resultKeys:          map[string]bool{},
		config: plannerConfig{
			count:                     1,
			maxVehicleLegs:            1,
			originDepartureBoardCount: 1,
			maxLabelsPerState:         1,
		},
		searchEndTime:   start.Add(time.Hour),
		searchDeadline:  time.Now().Add(time.Hour),
		destinationStop: destination,
	}
	runtime.departureBoardLookup = func(query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
		runtime.searchDeadline = time.Now().Add(-time.Second)
		return []*ctdf.DepartureBoard{{
			Time: start.Add(time.Minute),
			Journey: &ctdf.Journey{
				PrimaryIdentifier: "direct",
				Path: []*ctdf.JourneyPathItem{{
					OriginStopRef:          origin.PrimaryIdentifier,
					DestinationStopRef:     destination.PrimaryIdentifier,
					OriginDepartureTime:    start.Add(time.Minute),
					DestinationArrivalTime: start.Add(30 * time.Minute),
				}},
			},
		}}, nil
	}
	results := &ctdf.JourneyPlanResults{}
	if err := runtime.expandDepartures(&plannerPriorityQueue{}, &plannerLabel{stop: origin, arrivalTime: start}, destination, results); err != nil {
		t.Fatalf("expand direct departure: %v", err)
	}
	if len(results.JourneyPlans) != 1 {
		t.Fatalf("direct results after exhausted lookup budget = %d, want 1", len(results.JourneyPlans))
	}
}

func TestPlannerPrioritizesCambridgeRailConnectionToSheffieldOverLocalLabels(t *testing.T) {
	start := time.Date(2026, 8, 10, 16, 30, 0, 0, time.UTC)
	cambridgeLocation := &ctdf.Location{Type: "Point", Coordinates: []float64{0.137, 52.194}}
	londonLocation := &ctdf.Location{Type: "Point", Coordinates: []float64{-0.123, 51.531}}
	sheffieldLocation := &ctdf.Location{Type: "Point", Coordinates: []float64{-1.462, 53.378}}
	cambridge := &ctdf.Stop{PrimaryIdentifier: "cambridge", Location: cambridgeLocation, TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	shepreth := &ctdf.Stop{PrimaryIdentifier: "shepreth", Location: &ctdf.Location{Type: "Point", Coordinates: []float64{0.132, 52.114}}, TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	royston := &ctdf.Stop{PrimaryIdentifier: "royston", Location: &ctdf.Location{Type: "Point", Coordinates: []float64{-0.026, 52.053}}, TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	kingsCross := &ctdf.Stop{PrimaryIdentifier: "kings-cross", Location: londonLocation, TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	stPancras := &ctdf.Stop{PrimaryIdentifier: "st-pancras", Location: londonLocation, TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}
	sheffield := &ctdf.Stop{PrimaryIdentifier: "sheffield", Location: sheffieldLocation, TransportTypes: []ctdf.TransportType{ctdf.TransportTypeRail}}

	boardLookups := []string{}
	runtime := &plannerRuntime{
		stopCache: map[string]*ctdf.Stop{
			cambridge.PrimaryIdentifier:  cambridge,
			shepreth.PrimaryIdentifier:   shepreth,
			royston.PrimaryIdentifier:    royston,
			kingsCross.PrimaryIdentifier: kingsCross,
			stPancras.PrimaryIdentifier:  stPancras,
			sheffield.PrimaryIdentifier:  sheffield,
		},
		transferCache: map[string][]*ctdf.StopTransfer{
			cambridge.PrimaryIdentifier: {},
			shepreth.PrimaryIdentifier:  {},
			royston.PrimaryIdentifier:   {},
			kingsCross.PrimaryIdentifier: {
				{
					FromStopRef:          kingsCross.PrimaryIdentifier,
					ToStopRef:            stPancras.PrimaryIdentifier,
					Type:                 ctdf.StopTransferTypeNearbyWalk,
					TotalDurationSeconds: 300,
				},
			},
			stPancras.PrimaryIdentifier: {},
		},
		departureBoardCache: map[string]cachedDepartureBoard{},
		departureBoardLookup: func(q query.DepartureBoard) ([]*ctdf.DepartureBoard, error) {
			boardLookups = append(boardLookups, q.Stop.PrimaryIdentifier)
			switch q.Stop.PrimaryIdentifier {
			case cambridge.PrimaryIdentifier:
				return []*ctdf.DepartureBoard{{
					Time: start.Add(20 * time.Minute),
					Journey: &ctdf.Journey{
						PrimaryIdentifier: "cambridge-kings-cross",
						Path: []*ctdf.JourneyPathItem{
							{
								OriginStopRef:          cambridge.PrimaryIdentifier,
								DestinationStopRef:     shepreth.PrimaryIdentifier,
								OriginDepartureTime:    start.Add(20 * time.Minute),
								DestinationArrivalTime: start.Add(30 * time.Minute),
							},
							{
								OriginStopRef:          shepreth.PrimaryIdentifier,
								DestinationStopRef:     royston.PrimaryIdentifier,
								OriginDepartureTime:    start.Add(31 * time.Minute),
								DestinationArrivalTime: start.Add(40 * time.Minute),
							},
							{
								OriginStopRef:          royston.PrimaryIdentifier,
								DestinationStopRef:     kingsCross.PrimaryIdentifier,
								OriginDepartureTime:    start.Add(41 * time.Minute),
								DestinationArrivalTime: start.Add(100 * time.Minute),
							},
						},
					},
				}}, nil
			case stPancras.PrimaryIdentifier:
				return []*ctdf.DepartureBoard{{
					Time: start.Add(110 * time.Minute),
					Journey: &ctdf.Journey{
						PrimaryIdentifier: "st-pancras-sheffield",
						Path: []*ctdf.JourneyPathItem{{
							OriginStopRef:          stPancras.PrimaryIdentifier,
							DestinationStopRef:     sheffield.PrimaryIdentifier,
							OriginDepartureTime:    start.Add(110 * time.Minute),
							DestinationArrivalTime: start.Add(250 * time.Minute),
						}},
					},
				}}, nil
			default:
				return nil, nil
			}
		},
		bestArrivals: map[plannerStateKey][]time.Time{},
		resultKeys:   map[string]bool{},
		config: plannerConfig{
			count:                     1,
			maxVehicleLegs:            4,
			departureBoardCount:       12,
			originDepartureBoardCount: 12,
			maxTransferDistance:       1000,
			maxRouteItems:             10,
			maxConsecutiveTransfers:   1,
			maxLabelsPerState:         1,
		},
		searchEndTime:   start.Add(6 * time.Hour),
		destinationStop: sheffield,
	}
	pq := &plannerPriorityQueue{}
	for index := 0; index < 500; index++ {
		localStop := &ctdf.Stop{
			PrimaryIdentifier: fmt.Sprintf("local-%d", index),
			Location:          cambridgeLocation,
			TransportTypes:    []ctdf.TransportType{ctdf.TransportTypeBus},
		}
		runtime.stopCache[localStop.PrimaryIdentifier] = localStop
		runtime.transferCache[localStop.PrimaryIdentifier] = nil
		runtime.pushLabel(pq, &plannerLabel{stop: localStop, arrivalTime: start.Add(time.Minute), vehicleLegs: 1})
	}
	runtime.pushLabel(pq, &plannerLabel{
		stop:        cambridge,
		arrivalTime: start.Add(10 * time.Minute),
		vehicleLegs: 1,
		routeItems: appendRouteItem(nil, ctdf.JourneyPlanRouteItem{
			Type:               ctdf.JourneyPlanRouteItemTypeJourney,
			OriginStopRef:      "location-access",
			DestinationStopRef: cambridge.PrimaryIdentifier,
			StartTime:          start,
			ArrivalTime:        start.Add(10 * time.Minute),
			Journey:            &ctdf.Journey{PrimaryIdentifier: "access-bus"},
		}),
	})
	results := &ctdf.JourneyPlanResults{}
	expanded := 0
	for pq.Len() > 0 && len(results.JourneyPlans) == 0 && expanded < 10 {
		current := heap.Pop(pq).(*plannerLabel)
		if !runtime.isCurrentLabel(current) {
			continue
		}
		expanded++
		if !current.departuresExpanded && current.vehicleLegs < runtime.config.maxVehicleLegs {
			if err := runtime.expandDepartures(pq, current, sheffield, results); err != nil {
				t.Fatalf("departure expansion failed: %s", err)
			}
		}
		if len(results.JourneyPlans) > 0 {
			break
		}
		if !current.transfersExpanded && current.consecutiveTransfers < runtime.config.maxConsecutiveTransfers {
			if err := runtime.expandTransfers(pq, current, sheffield, results); err != nil {
				t.Fatalf("transfer expansion failed: %s", err)
			}
		}
	}
	if len(results.JourneyPlans) != 1 {
		t.Fatalf("rail route was starved by local labels after %d expansions", expanded)
	}
	for _, stopRef := range boardLookups {
		if stopRef == shepreth.PrimaryIdentifier || stopRef == royston.PrimaryIdentifier {
			t.Fatalf("intermediate rail stop %q was expanded before the terminal connection: %v", stopRef, boardLookups)
		}
	}
	items := results.JourneyPlans[0].RouteItems
	if len(items) != 4 || items[1].DestinationStopRef != kingsCross.PrimaryIdentifier || items[2].DestinationStopRef != stPancras.PrimaryIdentifier || items[3].DestinationStopRef != sheffield.PrimaryIdentifier {
		t.Fatalf("unexpected Cambridge to Sheffield route: %+v", items)
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
