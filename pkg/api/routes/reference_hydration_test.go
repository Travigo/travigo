package routes

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/gofiber/fiber/v2"
	"github.com/redis/go-redis/v9"
	"github.com/travigo/travigo/pkg/ctdf"
	"github.com/travigo/travigo/pkg/dataaggregator"
	"github.com/travigo/travigo/pkg/dataaggregator/source/databaselookup"
	"github.com/travigo/travigo/pkg/database"
	"github.com/travigo/travigo/pkg/redis_client"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo/integration/mtest"
)

func useMockReferences(mt *mtest.T) {
	old := database.Instance
	database.Instance = &database.MongoInstance{Client: mt.Client, Database: mt.DB}
	mt.Cleanup(func() { database.Instance = old })
}
func reply(mt *mtest.T, collection string, documents ...bson.D) {
	mt.AddMockResponses(mtest.CreateCursorResponse(0, mt.DB.Name()+"."+collection, mtest.FirstBatch, documents...))
}
func TestPlannerReferencesBatchedAcrossPlans(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("aliases platforms and misses", func(mt *mtest.T) {
		useMockReferences(mt)
		reply(mt, "stops", bson.D{{Key: "primaryidentifier", Value: "transfer"}, {Key: "otheridentifiers", Value: bson.A{"transfer-alias"}}, {Key: "platforms", Value: bson.A{bson.D{{Key: "primaryidentifier", Value: "platform"}}}}, {Key: "primaryname", Value: "Transfer"}}, bson.D{{Key: "primaryidentifier", Value: "origin"}, {Key: "otheridentifiers", Value: bson.A{"origin-alias"}}})
		reply(mt, "services", bson.D{{Key: "primaryidentifier", Value: "service"}, {Key: "otheridentifiers", Value: bson.A{"service-alias"}}, {Key: "routes", Value: bson.A{bson.D{{Key: "origin", Value: "Keep full planner fields"}}}}})
		reply(mt, "operators", bson.D{{Key: "primaryidentifier", Value: "operator"}, {Key: "otheridentifiers", Value: bson.A{"operator-alias"}}})
		plans := &ctdf.JourneyPlanResults{OriginStop: ctdf.Stop{PrimaryIdentifier: "origin", OtherIdentifiers: []string{"origin-alias"}}, DestinationStop: ctdf.Stop{PrimaryIdentifier: "destination"}}
		for i := 0; i < 30; i++ {
			plans.JourneyPlans = append(plans.JourneyPlans, ctdf.JourneyPlan{RouteItems: []ctdf.JourneyPlanRouteItem{{Journey: &ctdf.Journey{ServiceRef: "service-alias", OperatorRef: "operator-alias"}, OriginStopRef: "origin-alias", DestinationStopRef: "platform"}, {OriginStopRef: "transfer-alias", DestinationStopRef: "missing"}}})
		}
		hydratePlannerReferences(plans)
		events := mt.GetAllStartedEvents()
		if len(events) != 3 {
			mt.Fatalf("got %d database commands, want 3", len(events))
		}
		for _, plan := range plans.JourneyPlans {
			first := plan.RouteItems[0]
			if first.OriginStop != &plans.OriginStop || first.DestinationStop == nil || first.DestinationStop.PrimaryIdentifier != "transfer" || first.Journey.Operator == nil || first.Journey.Service == nil || len(first.Journey.Service.Routes) != 1 || plan.RouteItems[1].DestinationStop != nil {
				mt.Fatalf("incorrect enrichment: %+v", plan)
			}
		}
		if plans.JourneyPlans[0].RouteItems[0].Journey.Service != plans.JourneyPlans[29].RouteItems[0].Journey.Service {
			mt.Fatal("service not shared within request")
		}
	})
}
func TestBoardDisplayBatchAndProjection(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("selected arrival and shortened displays", func(mt *mtest.T) {
		useMockReferences(mt)
		reply(mt, "services", bson.D{{Key: "primaryidentifier", Value: "service"}, {Key: "stopnameoverrides", Value: bson.M{"platform": "Service Name"}}})
		reply(mt, "stops", bson.D{{Key: "primaryidentifier", Value: "stop"}, {Key: "primaryname", Value: "Base Name"}, {Key: "platforms", Value: bson.A{bson.D{{Key: "primaryidentifier", Value: "platform"}}}}})
		var board []*ctdf.DepartureBoard
		for i := 0; i < 30; i++ {
			board = append(board, &ctdf.DepartureBoard{Journey: &ctdf.Journey{ServiceRef: "service"}, DestinationDisplay: "fallback", DisplayReference: &ctdf.BoardDisplayReference{StopRef: "platform", Arrival: i%2 == 0}})
		}
		board = append(board, &ctdf.DepartureBoard{Journey: &ctdf.Journey{ServiceRef: "service"}, DestinationDisplay: "origin-missing", DisplayReference: &ctdf.BoardDisplayReference{StopRef: "missing", Arrival: true}})
		_, overrides := resolveDepartureBoardDestinationDisplays(board)
		if overrides != 30 {
			mt.Fatalf("got %d overrides", overrides)
		}
		for _, item := range board[:30] {
			if item.DestinationDisplay != "Service Name" {
				mt.Fatal("platform override lost")
			}
		}
		if board[30].DestinationDisplay != "missing" {
			mt.Fatal("arrival fallback lost")
		}
		// Reusing attached services must not trigger another service query, even for
		// multiple entries; this fixture has no operators to hydrate.
		hydrateDepartureBoardOperators(board)
		events := mt.GetAllStartedEvents()
		if len(events) != 2 {
			mt.Fatalf("got %d reads, want one service and one stop batch", len(events))
		}
		var cmd bson.M
		if err := bson.Unmarshal(events[0].Command, &cmd); err != nil {
			mt.Fatal(err)
		}
		projection := cmd["projection"].(bson.M)
		if projection["routes"] != nil || projection["stopnameoverrides"] != int32(1) || projection["brandcolour"] != int32(1) {
			mt.Fatalf("wrong service projection: %v", projection)
		}
		for _, groups := range [][]string{{"basic"}, {"web-board"}, {"departures-llm"}} {
			out := marshalSheriffMap(mt.T, groups, board[0])
			if _, exists := out["DisplayReference"]; exists {
				mt.Fatal("internal display metadata leaked")
			}
		}
	})
}
func TestStopBatchPrefersPrimaryOverAlias(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("primary precedence", func(mt *mtest.T) {
		useMockReferences(mt)
		reply(mt, "stops", bson.D{{Key: "primaryidentifier", Value: "requested"}}, bson.D{{Key: "primaryidentifier", Value: "other"}, {Key: "otheridentifiers", Value: bson.A{"requested"}}})
		got := loadStopsByReferences(map[string]struct{}{"requested": {}, "absent": {}}, nil)
		if got["requested"] == nil || got["requested"].PrimaryIdentifier != "requested" {
			mt.Fatal("alias overwrote primary")
		}
		if value, present := got["absent"]; !present || value != nil {
			mt.Fatal("missing negative entry")
		}
		if !reflect.DeepEqual(got["other"].OtherIdentifiers, []string{"requested"}) {
			mt.Fatal("alias lost")
		}
	})
}

func TestRealtimeOnlySkipsOuterHydrationButKeepsNestedTracks(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("nested tracks", func(mt *mtest.T) {
		useMockReferences(mt)
		server := miniredis.RunT(mt.T)
		client := redis.NewClient(&redis.Options{Addr: server.Addr()})
		defer client.Close()
		oldClient := redis_client.Client
		redis_client.Client = client
		defer func() { redis_client.Client = oldClient }()
		oldAggregator := dataaggregator.GlobalAggregator
		dataaggregator.GlobalAggregator = dataaggregator.Aggregator{}
		dataaggregator.GlobalAggregator.RegisterSource(databaselookup.Source{})
		defer func() { dataaggregator.GlobalAggregator = oldAggregator }()
		app := fiber.New()
		app.Get("/:identifier", getJourney)
		for _, view := range []string{"", "&view=web"} {
			reply(mt, "journeys", bson.D{{Key: "primaryidentifier", Value: "journey"}, {Key: "serviceref", Value: "unused-service"}, {Key: "operatorref", Value: "unused-operator"}, {Key: "path", Value: bson.A{bson.D{{Key: "originstopref", Value: "unused-origin"}, {Key: "destinationstopref", Value: "unused-destination"}}}}})
			reply(mt, "journey_tracks", bson.D{{Key: "primaryidentifier", Value: "track"}, {Key: "track", Value: bson.A{bson.D{{Key: "type", Value: "Point"}, {Key: "coordinates", Value: bson.A{1.0, 52.0}}}}}})
			payload, _ := json.Marshal(&ctdf.RealtimeJourney{PrimaryIdentifier: "rt", Journey: &ctdf.Journey{PrimaryIdentifier: "nested", TrackRef: "track"}})
			server.Set("realtime-journey:mapping:travigo-journeyid:journey", "rt")
			server.Set("realtime-journey:details/rt", string(payload))
			mt.ClearEvents()
			response, err := app.Test(httptest.NewRequest("GET", "/journey?realtime_only=true"+view, nil))
			if err != nil {
				mt.Fatal(err)
			}
			body, _ := io.ReadAll(response.Body)
			response.Body.Close()
			if response.StatusCode != 200 {
				mt.Fatalf("response %d: %s", response.StatusCode, body)
			}
			if events := mt.GetAllStartedEvents(); len(events) != 2 {
				mt.Fatalf("expected static existence lookup and nested track read only, got %d", len(events))
			}
			var got map[string]any
			if err := json.Unmarshal(body, &got); err != nil {
				mt.Fatal(err)
			}
			if view == "" {
				nested := got["Journey"].(map[string]any)
				if len(nested["Track"].([]any)) != 1 {
					mt.Fatalf("nested tracks lost: %s", body)
				}
			}
		}
		reply(mt, "journeys")
		response, err := app.Test(httptest.NewRequest("GET", "/absent?realtime_only=true", nil))
		if err != nil {
			mt.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != 404 {
			mt.Fatal("missing static journey behavior changed")
		}
	})
}

func TestMissingShortenedDisplayRetainsTerminalFallback(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	mt.Run("missing shortened destination", func(mt *mtest.T) {
		useMockReferences(mt)
		reply(mt, "stops", bson.D{{Key: "primaryidentifier", Value: "terminal"}, {Key: "primaryname", Value: "Scheduled Terminal"}})
		item := &ctdf.DepartureBoard{Journey: &ctdf.Journey{Path: []*ctdf.JourneyPathItem{{DestinationStopRef: "terminal"}}}, DisplayReference: &ctdf.BoardDisplayReference{StopRef: "missing"}}
		resolveDepartureBoardDestinationDisplays([]*ctdf.DepartureBoard{item})
		if item.DestinationDisplay != "Scheduled Terminal" {
			mt.Fatalf("terminal fallback lost: %q", item.DestinationDisplay)
		}
	})
}

func TestPlannerEndpointPrimaryIDsCannotBeOverwritten(t *testing.T) {
	for _, alias := range []bool{true, false} {
		for _, reverse := range []bool{false, true} {
			t.Run(fmt.Sprintf("alias=%t/reverse=%t", alias, reverse), func(t *testing.T) {
				plans := &ctdf.JourneyPlanResults{OriginStop: ctdf.Stop{PrimaryIdentifier: "origin", PrimaryName: "Origin"}, DestinationStop: ctdf.Stop{PrimaryIdentifier: "destination", PrimaryName: "Destination"}}
				owner, target := &plans.DestinationStop, "origin"
				if reverse {
					owner, target = &plans.OriginStop, "destination"
				}
				if alias {
					owner.OtherIdentifiers = []string{target}
				} else {
					owner.Platforms = []*ctdf.StopPlatform{{PrimaryIdentifier: target}}
				}
				plans.JourneyPlans = []ctdf.JourneyPlan{{RouteItems: []ctdf.JourneyPlanRouteItem{{OriginStopRef: "origin", DestinationStopRef: "destination"}}}}
				hydratePlannerReferences(plans)
				got := plans.JourneyPlans[0].RouteItems[0]
				if got.OriginStop != &plans.OriginStop || got.DestinationStop != &plans.DestinationStop {
					t.Fatalf("endpoint primary overwritten: origin=%+v destination=%+v", got.OriginStop, got.DestinationStop)
				}
			})
		}
	}
}
func TestPlannerEndpointAliasAndPlatformCannotMaskOtherPrimary(t *testing.T) {
	mt := mtest.New(t, mtest.NewOptions().ClientType(mtest.Mock))
	for _, alias := range []bool{true, false} {
		mt.Run(fmt.Sprintf("alias=%t", alias), func(mt *mtest.T) {
			useMockReferences(mt)
			plans := &ctdf.JourneyPlanResults{OriginStop: ctdf.Stop{PrimaryIdentifier: "origin"}, DestinationStop: ctdf.Stop{PrimaryIdentifier: "destination"}}
			if alias {
				plans.OriginStop.OtherIdentifiers = []string{"exact"}
			} else {
				plans.OriginStop.Platforms = []*ctdf.StopPlatform{{PrimaryIdentifier: "exact"}}
			}
			plans.JourneyPlans = []ctdf.JourneyPlan{{RouteItems: []ctdf.JourneyPlanRouteItem{{OriginStopRef: "exact", DestinationStopRef: "destination"}}}}
			endpointDoc := bson.D{{Key: "primaryidentifier", Value: "origin"}}
			if alias {
				endpointDoc = append(endpointDoc, bson.E{Key: "otheridentifiers", Value: bson.A{"exact"}})
			} else {
				endpointDoc = append(endpointDoc, bson.E{Key: "platforms", Value: bson.A{bson.D{{Key: "primaryidentifier", Value: "exact"}}}})
			}
			reply(mt, "stops", bson.D{{Key: "primaryidentifier", Value: "exact"}, {Key: "primaryname", Value: "Exact Stop"}}, endpointDoc)
			hydratePlannerReferences(plans)
			got := plans.JourneyPlans[0].RouteItems[0]
			if got.OriginStop == nil || got.OriginStop.PrimaryIdentifier != "exact" || got.OriginStop.PrimaryName != "Exact Stop" {
				mt.Fatalf("endpoint masked exact match: %+v", got.OriginStop)
			}
			if len(mt.GetAllStartedEvents()) != 1 {
				mt.Fatal("expected one shared stop query")
			}
		})
	}
}
